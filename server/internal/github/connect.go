package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

const ownershipRefusal = "You don't control this GitHub installation. Ask an owner or admin of the account to connect it."

func (s *Service) stateMAC(payload string) []byte {
	mac := hmac.New(sha256.New, s.StateSecret)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func (s *Service) InstallURL(ws, user uuid.UUID) (string, error) {
	if len(s.StateSecret) == 0 || ws == uuid.Nil || user == uuid.Nil {
		return "", errors.New("missing GitHub setup identity")
	}
	u, err := endpoint(s.Config.webURL(), "/apps/"+url.PathEscape(s.Config.Slug)+"/installations/new")
	if err != nil {
		return "", err
	}
	expiry := s.currentTime().Add(10 * time.Minute).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s|%s|%d|%s", ws, user, expiry, uuid.NewString())))
	state := payload + "." + base64.RawURLEncoding.EncodeToString(s.stateMAC(payload))
	u.RawQuery = url.Values{"state": {state}}.Encode()
	return u.String(), nil
}

func (s *Service) verifyState(state string) (uuid.UUID, uuid.UUID, error) {
	invalid := &UserError{"This GitHub connection request is invalid or expired. Connect again."}
	payload, tag, ok := strings.Cut(state, ".")
	mac, err := base64.RawURLEncoding.Strict().DecodeString(tag)
	if !ok || len(s.StateSecret) == 0 || err != nil || !hmac.Equal(mac, s.stateMAC(payload)) {
		return uuid.Nil, uuid.Nil, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	parts := strings.Split(string(raw), "|")
	if err != nil || len(parts) != 4 || parts[3] == "" {
		return uuid.Nil, uuid.Nil, invalid
	}
	ws, werr := uuid.Parse(parts[0])
	user, uerr := uuid.Parse(parts[1])
	expiry, eerr := strconv.ParseInt(parts[2], 10, 64)
	if werr != nil || uerr != nil || eerr != nil || ws == uuid.Nil || user == uuid.Nil || s.currentTime().Unix() >= expiry {
		return uuid.Nil, uuid.Nil, invalid
	}
	return ws, user, nil
}

// CheckSetupSession binds the signed install request to the returning browser's session.
func (s *Service) CheckSetupSession(state string, user uuid.UUID) error {
	_, expected, err := s.verifyState(state)
	if err != nil {
		return err
	}
	if user != expected {
		return &UserError{"Sign in to Lykeion in this browser with the account that started the GitHub connection, then connect again."}
	}
	return nil
}

func (s *Service) exchangeCode(ctx context.Context, code string) (_ string, err error) {
	defer func() { err = scrub(err, code, s.Config.ClientSecret) }()
	target, err := endpoint(s.Config.webURL(), "/login/oauth/access_token")
	if err != nil {
		return "", err
	}
	form := url.Values{"client_id": {s.Config.ClientID}, "client_secret": {s.Config.ClientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("invalid GitHub OAuth request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", s.apiError(resp)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.AccessToken == "" {
		return "", errors.New("GitHub code exchange failed")
	}
	return out.AccessToken, nil
}

type installationAccount struct {
	ID        int64
	Login     string
	Type      string
	AvatarURL string `json:"avatar_url"`
}

func (s *Service) repositoryPermissions(ctx context.Context, token, path string) (map[int64]bool, error) {
	repos := make(map[int64]bool)
	seen := make(map[string]bool)
	total := -1
	for path != "" {
		if seen[path] {
			return nil, errors.New("GitHub repository pagination repeated a page")
		}
		seen[path] = true
		var page struct {
			Total        *int `json:"total_count"`
			Repositories []struct {
				ID          int64
				Permissions struct{ Admin bool }
			}
		}
		next, err := s.getPage(ctx, token, path, &page)
		if err != nil {
			return nil, err
		}
		if page.Total == nil || *page.Total < 0 || page.Repositories == nil || (total >= 0 && total != *page.Total) {
			return nil, errors.New("incomplete GitHub repository list")
		}
		total = *page.Total
		for _, repo := range page.Repositories {
			if _, duplicate := repos[repo.ID]; duplicate || repo.ID <= 0 {
				return nil, errors.New("invalid GitHub repository list")
			}
			repos[repo.ID] = repo.Permissions.Admin
		}
		path = next
	}
	if len(repos) != total {
		return nil, errors.New("incomplete GitHub repository list")
	}
	return repos, nil
}

func (s *Service) ownedAccount(ctx context.Context, userToken string, installationID int64) (installationAccount, error) {
	var user struct{ ID int64 }
	var installation struct{ Account installationAccount }
	if err := s.get(ctx, userToken, "/user", &user); err != nil {
		return installationAccount{}, err
	}
	jwt, err := s.appJWT()
	if err != nil {
		return installationAccount{}, err
	}
	if err := s.get(ctx, jwt, fmt.Sprintf("/app/installations/%d", installationID), &installation); err != nil {
		return installationAccount{}, err
	}
	account := installation.Account
	if account.ID <= 0 || user.ID <= 0 || account.Login == "" {
		return installationAccount{}, errors.New("invalid GitHub account")
	}
	switch account.Type {
	case "User":
		if account.ID == user.ID {
			return account, nil
		}
	case "Organization":
		token, err := s.installationToken(ctx, installationID)
		if err != nil {
			return installationAccount{}, err
		}
		installed, err := s.repositoryPermissions(ctx, token, "/installation/repositories?per_page=100")
		if err != nil {
			return installationAccount{}, err
		}
		if len(installed) == 0 {
			return installationAccount{}, &UserError{"Select at least one repository on GitHub, then connect again."}
		}
		visible, err := s.repositoryPermissions(ctx, userToken, fmt.Sprintf("/user/installations/%d/repositories?per_page=100", installationID))
		if err != nil {
			return installationAccount{}, err
		}
		for id := range installed {
			if !visible[id] {
				return installationAccount{}, &UserError{ownershipRefusal}
			}
		}
		return account, nil
	}
	return installationAccount{}, &UserError{ownershipRefusal}
}

func (s *Service) Complete(ctx context.Context, state, code string, installationID int64) (uuid.UUID, error) {
	ws, user, err := s.verifyState(state)
	if err != nil {
		return uuid.Nil, err
	}
	if !s.Config.CanConnect() || code == "" || installationID <= 0 {
		return uuid.Nil, errors.New("invalid GitHub setup request")
	}
	token, err := s.exchangeCode(ctx, code)
	if err != nil {
		return uuid.Nil, err
	}
	account, err := s.ownedAccount(ctx, token, installationID)
	if err != nil {
		return uuid.Nil, err
	}
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
			return err
		}
		member, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: user})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && member.Role != "owner") {
			return &UserError{"Only a workspace owner can connect GitHub."}
		}
		if err != nil {
			return err
		}
		_, err = tx.Q.UpsertGithubConnection(ctx, db.UpsertGithubConnectionParams{
			WorkspaceID: ws, InstallationID: installationID, AccountLogin: account.Login,
			AccountType: account.Type, AccountAvatarUrl: account.AvatarURL, ConnectedBy: user,
		})
		if err != nil {
			return err
		}
		return tx.Emit(ctx, "github.changed", nil, store.User(user), nil)
	})
	if err != nil {
		return uuid.Nil, err
	}
	go s.Import(context.WithoutCancel(ctx), ws, installationID)
	return ws, nil
}

// Import starts the open pull request import after the connection commits.
func (s *Service) Import(context.Context, uuid.UUID, int64) {}
