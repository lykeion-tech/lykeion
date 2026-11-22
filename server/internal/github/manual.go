package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var repositoryNotConnected = &UserError{"That repository isn't connected to this workspace."}

// LinkByURL returns the linked row from the same transaction as the explicit link.
func (s *Service) LinkByURL(ctx context.Context, ws, user, card uuid.UUID, raw string) (row db.ListPullRequestsForCardRow, err error) {
	var connections []db.GithubConnection
	err = s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if err := checkStudyMember(ctx, tx, user, card); err != nil {
			return err
		}
		var err error
		connections, err = tx.Q.ListGithubConnections(ctx, ws)
		return err
	})
	if err != nil {
		return row, err
	}
	owner, name, number, ok := ParsePRURL(s.Config.webURL(), raw)
	if !ok {
		return row, &UserError{"Paste a GitHub pull request link."}
	}
	active := make(map[int64]time.Time)
	for _, c := range connections {
		if c.Status == "active" {
			active[c.InstallationID] = c.CreatedAt
		}
	}
	if len(active) == 0 {
		return row, repositoryNotConnected
	}
	app, err := s.appJWT()
	if err != nil {
		return row, err
	}
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	var installation struct{ ID int64 }
	if err := s.get(ctx, app, base+"/installation", &installation); err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == 404 {
			return row, repositoryNotConnected
		}
		return row, err
	}
	generation, ok := active[installation.ID]
	if !ok {
		return row, repositoryNotConnected
	}
	token, err := s.installationToken(ctx, installation.ID)
	if err != nil {
		return row, err
	}
	var detail pullRequestData
	if err := s.get(ctx, token, fmt.Sprintf("%s/pulls/%d", base, number), &detail); err != nil {
		return row, err
	}
	if int(detail.Number) != number || (detail.State != "open" && detail.State != "closed") || detail.CreatedAt.IsZero() || detail.UpdatedAt.IsZero() {
		return row, errors.New("invalid GitHub pull request")
	}
	repo := Repo{Name: name}
	repo.Owner.Login = owner
	pr := detail.pull(repo)
	err = s.withMirrorLock(ctx, installation.ID, pr, func(conn *pgxpool.Conn) error {
		targets, err := s.mirrorTargets(ctx, conn, installation.ID, pr)
		if err != nil {
			return err
		}
		for _, target := range targets {
			if target.workspace != ws {
				continue
			}
			target.manualCard = card
			return s.Store.InWorkspaceOn(ctx, conn, ws, user, func(tx *store.Tx) error {
				if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
					return err
				}
				if err := checkStudyMember(ctx, tx, user, card); err != nil {
					return err
				}
				c, err := tx.Q.LockActiveGithubConnection(ctx, db.LockActiveGithubConnectionParams{WorkspaceID: ws, InstallationID: installation.ID})
				if errors.Is(err, pgx.ErrNoRows) || err == nil && !c.CreatedAt.Equal(generation) {
					return repositoryNotConnected
				}
				if err != nil {
					return err
				}
				if err := applyMirror(ctx, tx, installation.ID, pr, target); err != nil && !errors.Is(err, errStaleMirror) {
					return err
				}
				stored, err := tx.Q.GetGithubPullRequestByRepo(ctx, db.GetGithubPullRequestByRepoParams{WorkspaceID: ws, RepoOwner: owner, RepoName: name, Number: detail.Number})
				if err != nil {
					return err
				}
				if stored.InstallationID != installation.ID {
					return repositoryNotConnected
				}
				row, err = linkStudyPR(ctx, tx, user, card, stored.ID)
				return err
			})
		}
		return repositoryNotConnected
	})
	return row, err
}

func checkStudyMember(ctx context.Context, tx *store.Tx, user, card uuid.UUID) error {
	if _, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: user}); err != nil {
		return err
	}
	_, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: card})
	return err
}

func linkStudyPR(ctx context.Context, tx *store.Tx, user, card, pr uuid.UUID) (db.ListPullRequestsForCardRow, error) {
	var row db.ListPullRequestsForCardRow
	n, err := tx.Q.InsertGithubLink(ctx, db.InsertGithubLinkParams{WorkspaceID: tx.WS, CardID: card, PrID: pr, Source: "manual", LinkedBy: &user})
	if err != nil {
		return row, err
	}
	if err = tx.Q.DeleteGithubUnlink(ctx, db.DeleteGithubUnlinkParams{WorkspaceID: tx.WS, CardID: card, PrID: pr}); err != nil {
		return row, err
	}
	if n > 0 {
		if err := tx.Emit(ctx, "github.pr.changed", &card, store.User(user), nil); err != nil {
			return row, err
		}
	}
	rows, err := tx.Q.ListPullRequestsForCard(ctx, db.ListPullRequestsForCardParams{WorkspaceID: tx.WS, CardID: card, PrID: &pr})
	if err != nil {
		return row, err
	}
	if len(rows) != 1 {
		return row, pgx.ErrNoRows
	}
	return rows[0], nil
}
