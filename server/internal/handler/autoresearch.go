package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/seal"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var errForbidden = errors.New("only the autoresearch's owner, its collaborators and workspace admins may do this")

type conflict string

func (e conflict) Error() string { return string(e) }

// answer writes the error a route ended with, or reports success.
func answer(w http.ResponseWriter, r *http.Request, err error) bool {
	var br cardInputError
	var cf conflict
	switch {
	case err == nil:
		return true
	case errors.As(err, &br):
		writeErr(w, 400, string(br))
	case errors.Is(err, errForbidden):
		writeErr(w, 403, err.Error())
	case errors.As(err, &cf):
		writeErr(w, 409, string(cf))
	default:
		fail(w, r, err)
	}
	return false
}

func canEdit(ctx context.Context, tx *store.Tx, ap db.Autoresearch, user uuid.UUID, role string) (bool, error) {
	if ap.OwnerID == user || role == "owner" || role == "admin" {
		return true, nil
	}
	return tx.Q.IsCollaborator(ctx, db.IsCollaboratorParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID, UserID: user})
}

// autoresearchTx loads the autoresearch named by {id} and runs fn with it. With
// edit set, only those with edit rights get through. The autoresearch is looked
// up before anything else, so a missing one, or one in another workspace, is
// 404 whatever the request carries.
func (h *H) autoresearchTx(w http.ResponseWriter, r *http.Request, edit bool, fn func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error) bool {
	id, ok := cardID(w, r)
	if !ok {
		return false
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
				return err
			}
			if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
				return err
			}
		}
		ap, err := tx.Q.GetAutoresearch(r.Context(), db.GetAutoresearchParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if edit {
			ok, err := canEdit(r.Context(), tx, ap, user, middleware.WorkspaceFrom(r.Context()).Role)
			if err != nil {
				return err
			}
			if !ok {
				return errForbidden
			}
		}
		return fn(tx, ap, user)
	})
	return answer(w, r, err)
}

type autoresearchIn struct {
	Title         *string  `json:"title"`
	Description   *string  `json:"description"`
	ExecutionMode string   `json:"execution_mode"`
	TitleTemplate *string  `json:"title_template"`
	BudgetUSD     *float64 `json:"budget_usd"`
}

// apply checks the fields given and writes them onto ap.
func (in autoresearchIn) apply(ap *db.Autoresearch) error {
	if in.Title != nil {
		ap.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		ap.Description = *in.Description
	}
	if in.TitleTemplate != nil {
		ap.TitleTemplate = strings.TrimSpace(*in.TitleTemplate)
	}
	if in.BudgetUSD != nil {
		ap.BudgetUsd = *in.BudgetUSD
	}
	switch {
	case ap.Title == "" || len([]rune(ap.Title)) > 200 || strings.ContainsRune(ap.Title, 0):
		return cardInputError("title must be 1 to 200 characters")
	case len([]rune(ap.Description)) > 20000 || strings.ContainsRune(ap.Description, 0):
		return cardInputError("description must be at most 20000 characters, without NUL")
	case len([]rune(strings.ReplaceAll(ap.TitleTemplate, "{{date}}", "2006-01-02"))) > 200 || strings.ContainsRune(ap.TitleTemplate, 0):
		return cardInputError("title_template must be at most 200 characters")
	case ap.BudgetUsd < 0:
		return cardInputError("budget_usd cannot be negative")
	}
	return nil
}

func (h *H) listAutoresearches(w http.ResponseWriter, r *http.Request) {
	var out []db.Autoresearch
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out, err = tx.Q.ListAutoresearches(r.Context(), ws)
		return err
	})
	if out == nil {
		out = []db.Autoresearch{}
	}
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

func (h *H) createAutoresearch(w http.ResponseWriter, r *http.Request) {
	var in autoresearchIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid autoresearch: "+err.Error())
		return
	}
	if in.ExecutionMode != "create_card" && in.ExecutionMode != "run_only" {
		writeErr(w, 400, "execution_mode must be create_card or run_only")
		return
	}
	var ap db.Autoresearch
	if err := in.apply(&ap); err != nil {
		answer(w, r, err)
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		ap, err = tx.Q.CreateAutoresearch(r.Context(), db.CreateAutoresearchParams{WorkspaceID: ws, OwnerID: user, Title: ap.Title,
			Description: ap.Description, ExecutionMode: in.ExecutionMode, TitleTemplate: ap.TitleTemplate, BudgetUsd: ap.BudgetUsd})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "autoresearch.created", nil, store.User(user), map[string]any{"autoresearch_id": ap.ID, "title": ap.Title})
	})
	if answer(w, r, err) {
		writeJSON(w, 201, ap)
	}
}

type triggerView struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	Cron      string          `json:"cron"`
	Tz        string          `json:"tz"`
	NextRunAt *time.Time      `json:"next_run_at"`
	Provider  string          `json:"provider"`
	Filters   json.RawMessage `json:"filters"`
	Enabled   bool            `json:"enabled"`
	Signed    bool            `json:"signed"`
	CreatedBy uuid.UUID       `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

// viewTrigger leaves out the token hash and the sealed secret.
func viewTrigger(t db.AutoresearchTrigger) triggerView {
	return triggerView{ID: t.ID, Kind: t.Kind, Cron: t.Cron, Tz: t.Tz, NextRunAt: t.NextRunAt, Provider: t.Provider,
		Filters: t.Filters, Enabled: t.Enabled, Signed: t.SigningSecretEnc != nil, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt}
}

func (h *H) getAutoresearch(w http.ResponseWriter, r *http.Request) {
	out := struct {
		db.Autoresearch
		Triggers      []triggerView `json:"triggers"`
		Collaborators []uuid.UUID   `json:"collaborators"`
		Subscribers   []uuid.UUID   `json:"subscribers"`
	}{Triggers: []triggerView{}}
	ok := h.autoresearchTx(w, r, false, func(tx *store.Tx, ap db.Autoresearch, _ uuid.UUID) error {
		out.Autoresearch = ap
		ts, err := tx.Q.ListTriggers(r.Context(), db.ListTriggersParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID})
		if err != nil {
			return err
		}
		for _, t := range ts {
			out.Triggers = append(out.Triggers, viewTrigger(t))
		}
		if out.Collaborators, err = tx.Q.ListCollaborators(r.Context(), db.ListCollaboratorsParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID}); err != nil {
			return err
		}
		out.Subscribers, err = tx.Q.ListSubscribers(r.Context(), db.ListSubscribersParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID})
		return err
	})
	if ok {
		writeJSON(w, 200, out)
	}
}

func (h *H) updateAutoresearch(w http.ResponseWriter, r *http.Request) {
	var out db.Autoresearch
	ok := h.autoresearchTx(w, r, true, func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error {
		var in autoresearchIn
		if err := readJSON(r, &in); err != nil {
			return cardInputError("invalid autoresearch: " + err.Error())
		}
		if in.ExecutionMode != "" {
			return cardInputError("execution_mode cannot change; create another autoresearch")
		}
		if err := in.apply(&ap); err != nil {
			return err
		}
		var err error
		out, err = tx.Q.UpdateAutoresearch(r.Context(), db.UpdateAutoresearchParams{Title: ap.Title, Description: ap.Description,
			TitleTemplate: ap.TitleTemplate, BudgetUsd: ap.BudgetUsd, WorkspaceID: tx.WS, ID: ap.ID})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "autoresearch.updated", nil, store.User(user), map[string]any{"autoresearch_id": ap.ID})
	})
	if ok {
		writeJSON(w, 200, out)
	}
}

func (h *H) deleteAutoresearch(w http.ResponseWriter, r *http.Request) {
	ok := h.autoresearchTx(w, r, true, func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error {
		if err := tx.Q.DeleteAutoresearch(r.Context(), db.DeleteAutoresearchParams{WorkspaceID: tx.WS, ID: ap.ID}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "autoresearch.deleted", nil, store.User(user), map[string]any{"autoresearch_id": ap.ID})
	})
	if ok {
		w.WriteHeader(204)
	}
}

// reschedule sets every enabled schedule of the autoresearch to its first slot
// after now, so nothing missed while it was paused fires on resume.
func reschedule(ctx context.Context, tx *store.Tx, ap uuid.UUID, only *uuid.UUID) error {
	ts, err := tx.Q.ListTriggers(ctx, db.ListTriggersParams{WorkspaceID: tx.WS, AutoresearchID: ap})
	if err != nil {
		return err
	}
	for _, t := range ts {
		if t.Kind != "schedule" || (only != nil && t.ID != *only) {
			continue
		}
		next, err := autoresearch.NextRun(t.Cron, t.Tz, time.Now())
		if err != nil {
			continue // fire disables a schedule that no longer parses
		}
		if err := tx.Q.SetTriggerNextRun(ctx, db.SetTriggerNextRunParams{NextRunAt: &next, WorkspaceID: tx.WS, ID: t.ID}); err != nil {
			return err
		}
	}
	return nil
}

func (h *H) setAutoresearchStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var out db.Autoresearch
		ok := h.autoresearchTx(w, r, true, func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error {
			var in struct {
				Reason string `json:"reason"`
			}
			if r.ContentLength != 0 {
				if err := readJSON(r, &in); err != nil {
					return cardInputError("invalid body: " + err.Error())
				}
			}
			if strings.ContainsRune(in.Reason, 0) {
				return cardInputError("reason must not contain NUL")
			}
			if status == "active" {
				in.Reason = ""
				if err := reschedule(r.Context(), tx, ap.ID, nil); err != nil {
					return err
				}
			}
			var err error
			out, err = tx.Q.SetAutoresearchStatus(r.Context(), db.SetAutoresearchStatusParams{Status: status, PauseReason: in.Reason,
				WorkspaceID: tx.WS, ID: ap.ID})
			if err != nil {
				return err
			}
			event := map[string]string{"active": "autoresearch.resumed", "paused": "autoresearch.paused"}[status]
			return tx.Emit(r.Context(), event, nil, store.User(user), map[string]any{"autoresearch_id": ap.ID, "reason": in.Reason})
		})
		if ok {
			writeJSON(w, 200, out)
		}
	}
}

func (h *H) runAutoresearch(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	raw := middleware.FromHeader(r)
	if raw == "" {
		writeErr(w, 400, "X-Workspace-ID required")
		return
	}
	ws, err := uuid.Parse(raw)
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	who := user(r)
	var run db.AutoresearchRun
	err = h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(r.Context(), ws); err != nil {
			return err
		}
		m, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: who})
		if err != nil {
			return err
		}
		ap, err := tx.Q.GetAutoresearch(r.Context(), db.GetAutoresearchParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		allowed, err := canEdit(r.Context(), tx, ap, who, m.Role)
		if err != nil {
			return err
		}
		if !allowed {
			return errForbidden
		}
		if ap.Status != "active" {
			return conflict("the autoresearch is paused; resume it first")
		}
		run, _, err = h.Autoresearch.Admit(r.Context(), tx, autoresearch.Admission{Autoresearch: id, Source: "manual", Actor: who, PlannedAt: time.Now(), Key: "manual:" + uuid.NewString()})
		return err
	})
	if !answer(w, r, err) {
		return
	}
	if !answer(w, r, h.Autoresearch.Dispatch(r.Context(), ws, run.ID)) {
		return
	}
	err = h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
		var err error
		run, err = tx.Q.GetRun(r.Context(), db.GetRunParams{WorkspaceID: ws, ID: run.ID})
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 201, run)
	}
}

func (h *H) listAutoresearchRuns(w http.ResponseWriter, r *http.Request) {
	out := []db.AutoresearchRun{}
	ok := h.autoresearchTx(w, r, false, func(tx *store.Tx, ap db.Autoresearch, _ uuid.UUID) error {
		runs, err := tx.Q.ListRuns(r.Context(), db.ListRunsParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID})
		out = append(out, runs...)
		return err
	})
	if ok {
		writeJSON(w, 200, out)
	}
}

func (h *H) createTrigger(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	ok := h.autoresearchTx(w, r, true, func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error {
		var in struct {
			Kind          string          `json:"kind"`
			Cron          string          `json:"cron"`
			Tz            string          `json:"tz"`
			Provider      string          `json:"provider"`
			SigningSecret string          `json:"signing_secret"`
			Filters       json.RawMessage `json:"filters"`
		}
		if err := readJSON(r, &in); err != nil {
			return cardInputError("invalid trigger: " + err.Error())
		}
		if in.Tz == "" {
			u, err := tx.Q.GetUser(r.Context(), user)
			if err != nil {
				return err
			}
			in.Tz = u.Timezone
		}
		if _, err := autoresearch.Zone(in.Tz); err != nil {
			return cardInputError(err.Error())
		}
		p := db.CreateTriggerParams{ID: uuid.New(), WorkspaceID: tx.WS, AutoresearchID: ap.ID, Kind: in.Kind, Tz: in.Tz,
			Provider: "generic", Filters: json.RawMessage(`[]`), CreatedBy: user}
		token := ""
		switch in.Kind {
		case "schedule":
			if in.Provider != "" || in.SigningSecret != "" || len(in.Filters) > 0 {
				return cardInputError("a schedule takes only cron and tz")
			}
			next, err := autoresearch.NextRun(in.Cron, in.Tz, time.Now())
			if err != nil {
				return cardInputError(err.Error())
			}
			p.Cron, p.NextRunAt = strings.TrimSpace(in.Cron), &next
		case "webhook":
			if in.Cron != "" {
				return cardInputError("a webhook takes no cron")
			}
			if in.Provider != "" {
				p.Provider = in.Provider
			}
			if p.Provider != "generic" && p.Provider != "github" {
				return cardInputError("provider must be generic or github")
			}
			fs, err := autoresearch.ParseFilters(in.Filters)
			if err != nil {
				return cardInputError(err.Error())
			}
			p.Filters, err = json.Marshal(fs)
			if err != nil {
				return err
			}
			if in.SigningSecret != "" {
				if p.Provider != "github" {
					return cardInputError("only github webhooks are signed; a generic webhook relies on its URL token")
				}
				if len(in.SigningSecret) > 256 {
					return cardInputError("signing_secret must be at most 256 bytes")
				}
				sealed, err := seal.Seal(h.Autoresearch.Key, []byte(in.SigningSecret), p.ID[:])
				if errors.Is(err, seal.ErrNoKey) {
					return cardInputError("the server has no LYK_SECRET_KEY, so it cannot store a signing secret")
				}
				if err != nil {
					return err
				}
				p.SigningSecretEnc = sealed
			}
			var hash string
			token, hash, _ = auth.NewToken(autoresearch.WebhookPrefix)
			p.TokenHash = &hash
		default:
			return cardInputError("kind must be schedule or webhook")
		}
		t, err := tx.Q.CreateTrigger(r.Context(), p)
		if err != nil {
			return err
		}
		out["trigger"] = viewTrigger(t)
		if token != "" {
			out["token"], out["url"] = token, "/api/webhooks/autoresearch/"+token
		}
		return tx.Emit(r.Context(), "autoresearch_trigger.created", nil, store.User(user),
			map[string]any{"autoresearch_id": ap.ID, "trigger_id": t.ID, "kind": t.Kind})
	})
	if ok {
		writeJSON(w, 201, out)
	}
}

func triggerID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "trigger"))
	if err != nil {
		return id, pgx.ErrNoRows
	}
	return id, nil
}

func (h *H) setTriggerEnabled(w http.ResponseWriter, r *http.Request) {
	var out db.AutoresearchTrigger
	ok := h.autoresearchTx(w, r, true, func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error {
		id, err := triggerID(r)
		if err != nil {
			return err
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := readJSON(r, &in); err != nil {
			return cardInputError("invalid body: " + err.Error())
		}
		// A trigger of another autoresearch is refused after the update, which the
		// returned error rolls back.
		if out, err = tx.Q.SetTriggerEnabled(r.Context(), db.SetTriggerEnabledParams{Enabled: in.Enabled, WorkspaceID: tx.WS, ID: id}); err != nil {
			return err
		}
		if out.AutoresearchID != ap.ID {
			return pgx.ErrNoRows
		}
		if in.Enabled {
			if err := reschedule(r.Context(), tx, ap.ID, &id); err != nil {
				return err
			}
			out, err = tx.Q.GetTrigger(r.Context(), db.GetTriggerParams{WorkspaceID: tx.WS, ID: id})
			if err != nil {
				return err
			}
		}
		return tx.Emit(r.Context(), "autoresearch_trigger.updated", nil, store.User(user),
			map[string]any{"autoresearch_id": ap.ID, "trigger_id": id, "enabled": in.Enabled})
	})
	if ok {
		writeJSON(w, 200, viewTrigger(out))
	}
}

func (h *H) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	ok := h.autoresearchTx(w, r, true, func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error {
		id, err := triggerID(r)
		if err != nil {
			return err
		}
		n, err := tx.Q.DeleteTrigger(r.Context(), db.DeleteTriggerParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID, ID: id})
		if err != nil {
			return err
		}
		if n == 0 {
			return pgx.ErrNoRows
		}
		return tx.Emit(r.Context(), "autoresearch_trigger.deleted", nil, store.User(user), map[string]any{"autoresearch_id": ap.ID, "trigger_id": id})
	})
	if ok {
		w.WriteHeader(204)
	}
}

// people serves PUT and DELETE on collaborators and subscribers. A member may
// add or remove themselves as a subscriber; everything else needs edit rights.
func (h *H) people(collaborators, add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target, err := uuid.Parse(chi.URLParam(r, "user"))
		if err != nil {
			writeErr(w, 404, "not found")
			return
		}
		self := middleware.IdentityFrom(r.Context()).UserID == target
		ok := h.autoresearchTx(w, r, collaborators || !self, func(tx *store.Tx, ap db.Autoresearch, user uuid.UUID) error {
			ctx := r.Context()
			if _, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: tx.WS, UserID: target}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cardInputError("that user is not a member of this workspace")
				}
				return err
			}
			var err error
			switch {
			case collaborators && add:
				err = tx.Q.AddCollaborator(ctx, db.AddCollaboratorParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID, UserID: target})
			case collaborators:
				_, err = tx.Q.RemoveCollaborator(ctx, db.RemoveCollaboratorParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID, UserID: target})
			case add:
				err = tx.Q.AddSubscriber(ctx, db.AddSubscriberParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID, UserID: target})
			default:
				_, err = tx.Q.RemoveSubscriber(ctx, db.RemoveSubscriberParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID, UserID: target})
			}
			if err != nil {
				return err
			}
			event := "autoresearch.subscribers_changed"
			if collaborators {
				event = "autoresearch.collaborators_changed"
			}
			return tx.Emit(ctx, event, nil, store.User(user), map[string]any{"autoresearch_id": ap.ID, "user_id": target, "added": add})
		})
		if ok {
			w.WriteHeader(204)
		}
	}
}
