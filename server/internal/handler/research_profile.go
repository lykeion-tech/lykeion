package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const researchIdentityLimit = 4000

type researchIdentity struct {
	Identity string   `json:"identity"`
	Links    []string `json:"links"`
}

type researchProfile struct {
	Identity      *string  `json:"identity,omitempty"`
	Background    string   `json:"background"`
	Topics        string   `json:"topics"`
	Methods       string   `json:"methods"`
	Collaboration string   `json:"collaboration"`
	Links         []string `json:"links"`
}

func (p *researchProfile) normalize() error {
	type textField struct {
		name  string
		value *string
		limit int
	}
	backgroundLimit := 2000
	if p.Topics == "" && p.Methods == "" && p.Collaboration == "" {
		// Older clients receive the full free-form text in Background.
		backgroundLimit = researchIdentityLimit
	}
	fields := []textField{
		{"Background", &p.Background, backgroundLimit},
		{"Research topics", &p.Topics, 500},
		{"Methods and tools", &p.Methods, 500},
		{"Collaboration interests", &p.Collaboration, 500},
	}
	if p.Identity != nil {
		fields = []textField{{"Research identity", p.Identity, researchIdentityLimit}}
	}
	for _, field := range fields {
		*field.value = strings.TrimSpace(*field.value)
		if utf8.RuneCountInString(*field.value) > field.limit {
			return fmt.Errorf("%s can contain up to %d characters", field.name, field.limit)
		}
		if strings.ContainsFunc(*field.value, func(r rune) bool {
			return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
		}) {
			return fmt.Errorf("%s contains an unsupported character", field.name)
		}
	}
	if len(p.Links) > 5 {
		return errors.New("add up to 5 links")
	}
	if p.Links == nil {
		p.Links = []string{}
	}
	for i, link := range p.Links {
		link = strings.TrimSpace(link)
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
			utf8.RuneCountInString(link) > 512 || strings.ContainsFunc(link, unicode.IsSpace) {
			return errors.New("links must be HTTP or HTTPS URLs of up to 512 characters, without credentials")
		}
		p.Links[i] = link
	}
	return nil
}

func (p researchProfile) identityText() string {
	if p.Identity != nil {
		return *p.Identity
	}
	var sections []string
	if p.Background != "" {
		sections = append(sections, p.Background)
	}
	if p.Topics != "" {
		sections = append(sections, "Research topics: "+p.Topics)
	}
	if p.Methods != "" {
		sections = append(sections, "Methods and tools: "+p.Methods)
	}
	if p.Collaboration != "" {
		sections = append(sections, "Collaboration interests: "+p.Collaboration)
	}
	return strings.Join(sections, "\n\n")
}

// Keep the structured fields readable by installed clients. Revisions still
// describe the stored JSON so existing publication confirmations stay valid.
func researchProfileForClient(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var profile researchProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, err
	}
	if profile.Identity != nil {
		profile.Background = *profile.Identity
	} else {
		identity := profile.identityText()
		profile.Identity = &identity
	}
	return json.Marshal(profile)
}

type accountResearchProfile struct {
	Draft             json.RawMessage `json:"draft"`
	Published         json.RawMessage `json:"published"`
	DraftRevision     *string         `json:"draft_revision"`
	PublishedRevision *string         `json:"published_revision"`
	WorkspaceIDs      []uuid.UUID     `json:"workspace_ids"`
}

func researchProfileRevision(profile json.RawMessage) *string {
	if len(profile) == 0 {
		return nil
	}
	digest := sha256.Sum256(profile)
	revision := hex.EncodeToString(digest[:])
	return &revision
}

func readAccountResearchProfile(ctx context.Context, tx *store.Tx, user uuid.UUID) (accountResearchProfile, error) {
	row, err := tx.Q.GetAccountResearchProfile(ctx, user)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return accountResearchProfile{}, err
	}
	if row.WorkspaceIds == nil {
		row.WorkspaceIds = []uuid.UUID{}
	}
	draft, err := researchProfileForClient(row.Draft)
	if err != nil {
		return accountResearchProfile{}, err
	}
	published, err := researchProfileForClient(row.Published)
	if err != nil {
		return accountResearchProfile{}, err
	}
	return accountResearchProfile{
		Draft: draft, Published: published, WorkspaceIDs: row.WorkspaceIds,
		DraftRevision: researchProfileRevision(row.Draft), PublishedRevision: researchProfileRevision(row.Published),
	}, nil
}

func (h *H) getAccountResearchProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := middleware.IdentityFrom(ctx)
	var result accountResearchProfile
	err := h.Store.AsUser(ctx, id.UserID, id.Email, func(tx *store.Tx) error {
		var err error
		result, err = readAccountResearchProfile(ctx, tx, id.UserID)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (h *H) saveResearchProfileDraft(w http.ResponseWriter, r *http.Request) {
	h.storeResearchProfile(w, r, false)
}

func (h *H) saveResearchProfile(w http.ResponseWriter, r *http.Request) {
	h.storeResearchProfile(w, r, true)
}

func (h *H) storeResearchProfile(w http.ResponseWriter, r *http.Request, updatePublished bool) {
	var profile *researchProfile
	if err := readJSON(r, &profile); err != nil || profile == nil {
		writeErr(w, http.StatusBadRequest, "invalid research profile")
		return
	}
	if err := profile.normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var stored any = profile
	if profile.Identity != nil {
		stored = researchIdentity{Identity: *profile.Identity, Links: profile.Links}
	}
	data, err := json.Marshal(stored)
	if err != nil {
		fail(w, r, err)
		return
	}
	ctx := r.Context()
	id := middleware.IdentityFrom(ctx)
	var result accountResearchProfile
	var updated int64
	err = h.Store.AsUser(ctx, id.UserID, id.Email, func(tx *store.Tx) error {
		if err := tx.Q.SaveResearchProfileDraft(ctx, db.SaveResearchProfileDraftParams{UserID: id.UserID, Profile: data}); err != nil {
			return err
		}
		var err error
		if updatePublished {
			updated, err = tx.Q.PublishResearchProfileChanges(ctx, id.UserID)
			if err != nil {
				return err
			}
		}
		result, err = readAccountResearchProfile(ctx, tx, id.UserID)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if updated > 0 {
		h.notifyResearchProfileUpdated(ctx, id.UserID, result.WorkspaceIDs)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

var errResearchProfileAudienceChanged = errors.New("research profile sharing changed; review the workspaces and try again")
var errResearchProfileContentChanged = errors.New("research profile changed; review it before publishing")

func (h *H) publishResearchProfileChanges(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceIDs  []uuid.UUID `json:"workspace_ids"`
		DraftRevision string      `json:"draft_revision"`
	}
	if err := readJSON(r, &input); err != nil || len(input.WorkspaceIDs) == 0 || len(input.DraftRevision) != 2*sha256.Size {
		writeErr(w, http.StatusBadRequest, "confirm the research profile revision and publication workspaces")
		return
	}
	expected := make(map[uuid.UUID]struct{}, len(input.WorkspaceIDs))
	for _, ws := range input.WorkspaceIDs {
		if _, duplicate := expected[ws]; duplicate || ws == uuid.Nil {
			writeErr(w, http.StatusBadRequest, "invalid research profile publication audience")
			return
		}
		expected[ws] = struct{}{}
	}

	ctx := r.Context()
	id := middleware.IdentityFrom(ctx)
	var result accountResearchProfile
	err := h.Store.AsUser(ctx, id.UserID, id.Email, func(tx *store.Tx) error {
		if _, err := tx.Q.LockResearchProfileDraft(ctx, id.UserID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errResearchProfileAudienceChanged
			}
			return err
		}
		current, err := readAccountResearchProfile(ctx, tx, id.UserID)
		if err != nil {
			return err
		}
		if current.DraftRevision == nil || *current.DraftRevision != input.DraftRevision {
			return errResearchProfileContentChanged
		}
		if len(current.WorkspaceIDs) != len(expected) {
			return errResearchProfileAudienceChanged
		}
		for _, ws := range current.WorkspaceIDs {
			if _, confirmed := expected[ws]; !confirmed {
				return errResearchProfileAudienceChanged
			}
		}
		n, err := tx.Q.PublishResearchProfileChanges(ctx, id.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return errResearchProfileAudienceChanged
		}
		result, err = readAccountResearchProfile(ctx, tx, id.UserID)
		return err
	})
	if errors.Is(err, errResearchProfileAudienceChanged) || errors.Is(err, errResearchProfileContentChanged) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	h.notifyResearchProfileUpdated(ctx, id.UserID, result.WorkspaceIDs)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (h *H) notifyResearchProfileUpdated(ctx context.Context, user uuid.UUID, workspaces []uuid.UUID) {
	// Release the draft lock before taking workspace locks, which scoped mutations take first.
	eventCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, ws := range workspaces {
		if err := h.Store.InWorkspace(eventCtx, ws, user, func(tx *store.Tx) error {
			if _, err := tx.Q.LockWorkspaceForMutation(eventCtx, ws); err != nil {
				return err
			}
			if _, err := tx.Q.LockMember(eventCtx, db.LockMemberParams{WorkspaceID: ws, UserID: user}); err != nil {
				return err
			}
			return emitResearchProfileUpdated(eventCtx, tx, user)
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("research profile invalidation failed", "workspace", ws, "err", err)
		}
	}
}

func (h *H) shareResearchProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProfileRevision string `json:"profile_revision"`
	}
	if err := readJSON(r, &input); err != nil || len(input.ProfileRevision) != 2*sha256.Size {
		writeErr(w, http.StatusBadRequest, "confirm the research profile revision to share")
		return
	}
	h.setResearchProfilePublication(w, r, input.ProfileRevision)
}

func (h *H) unshareResearchProfile(w http.ResponseWriter, r *http.Request) {
	h.setResearchProfilePublication(w, r, "")
}

func (h *H) setResearchProfilePublication(w http.ResponseWriter, r *http.Request, revision string) {
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	ws, user := h.scope(r)
	share := revision != ""
	_, err := tx.Q.LockResearchProfileDraft(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		if share {
			writeErr(w, http.StatusConflict, "save a research profile draft before sharing it")
			return
		}
	} else if err != nil {
		fail(w, r, err)
		return
	}
	var changed int64
	if share {
		state, err := readAccountResearchProfile(ctx, tx, user)
		if err != nil {
			fail(w, r, err)
			return
		}
		current := state.PublishedRevision
		if current == nil {
			current = state.DraftRevision
		}
		if current == nil || *current != revision {
			writeErr(w, http.StatusConflict, errResearchProfileContentChanged.Error())
			return
		}
		if state.PublishedRevision == nil {
			var draft researchProfile
			if err := json.Unmarshal(state.Draft, &draft); err != nil {
				fail(w, r, err)
				return
			}
			if draft.identityText() == "" && len(draft.Links) == 0 {
				writeErr(w, http.StatusConflict, "add something to your research profile before sharing it")
				return
			}
		}
		if err := tx.Q.InitializeResearchProfileSnapshot(ctx, user); err != nil {
			fail(w, r, err)
			return
		}
		changed, err = tx.Q.ShareResearchProfile(ctx, db.ShareResearchProfileParams{WorkspaceID: ws, UserID: user})
	} else {
		changed, err = tx.Q.UnshareResearchProfile(ctx, db.UnshareResearchProfileParams{WorkspaceID: ws, UserID: user})
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	if changed > 0 {
		if err := emitResearchProfileUpdated(ctx, tx, user); err != nil {
			fail(w, r, err)
			return
		}
	}
	result, err := readAccountResearchProfile(ctx, tx, user)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func emitResearchProfileUpdated(ctx context.Context, tx *store.Tx, user uuid.UUID) error {
	return tx.Emit(ctx, "research_profile.updated", nil, store.User(user), map[string]uuid.UUID{"user_id": user})
}

func (h *H) getMemberResearchProfile(w http.ResponseWriter, r *http.Request) {
	target, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "member not found")
		return
	}
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	raw, err := tx.Q.GetMemberResearchProfile(ctx, db.GetMemberResearchProfileParams{WorkspaceID: tx.WS, UserID: target})
	if err != nil {
		fail(w, r, err)
		return
	}
	profile, err := researchProfileForClient(raw)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Profile json.RawMessage `json:"profile"`
	}{Profile: profile})
}
