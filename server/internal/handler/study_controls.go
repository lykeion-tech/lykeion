package handler

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) runReactions(w http.ResponseWriter, r *http.Request) {
	card, ok := cardID(w, r)
	if !ok {
		return
	}
	run, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var in struct {
		Emoji string `json:"emoji"`
	}
	if r.Method != http.MethodGet && (readJSON(r, &in) != nil || in.Emoji == "" || len(in.Emoji) > 64 || !utf8.ValidString(in.Emoji) || strings.IndexFunc(in.Emoji, func(c rune) bool { return unicode.IsControl(c) || unicode.IsSpace(c) }) >= 0) {
		writeErr(w, 400, "invalid reaction")
		return
	}
	out := []store.StudyReaction{}
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		t, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: ws, ID: run})
		if err != nil {
			return err
		}
		if t.CardID == nil || *t.CardID != card {
			return pgx.ErrNoRows
		}
		if r.Method != http.MethodGet {
			if err := tx.SetRunReaction(r.Context(), run, user, in.Emoji, r.Method == http.MethodPut); err != nil {
				return err
			}
			if err := tx.Emit(r.Context(), "study.run_reactions_changed", &card, store.User(user), map[string]any{"run_id": run}); err != nil {
				return err
			}
		}
		out, err = tx.RunReactions(r.Context(), run, user)
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

func (h *H) studyReactions(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		Emoji string `json:"emoji"`
	}
	if r.Method != http.MethodGet {
		if readJSON(r, &in) != nil || in.Emoji == "" || len(in.Emoji) > 64 || !utf8.ValidString(in.Emoji) || strings.IndexFunc(in.Emoji, func(c rune) bool { return unicode.IsControl(c) || unicode.IsSpace(c) }) >= 0 {
			writeErr(w, 400, "invalid reaction")
			return
		}
	}
	out := []store.StudyReaction{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		if r.Method != http.MethodGet {
			if err := tx.SetStudyReaction(r.Context(), id, user, in.Emoji, r.Method == http.MethodPut); err != nil {
				return err
			}
			if err := tx.Emit(r.Context(), "study.reactions_changed", &id, store.User(user), nil); err != nil {
				return err
			}
		}
		var err error
		out, err = tx.StudyReactions(r.Context(), id, user)
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

func (h *H) attachStudyFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		AttachmentIDs []uuid.UUID `json:"attachment_ids"`
	}
	if readJSON(r, &in) != nil || len(in.AttachmentIDs) == 0 {
		writeErr(w, 400, "attachment ids required")
		return
	}
	files, err := h.studyFiles(r, in.AttachmentIDs)
	if !answer(w, r, err) {
		return
	}
	var view studyView
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if err := tx.Q.WaitLockCard(r.Context(), id); err != nil {
			return err
		}
		c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		existing, err := tx.Q.CardAttachments(r.Context(), db.CardAttachmentsParams{WorkspaceID: ws, CardID: &id})
		if err != nil {
			return err
		}
		services, err := tx.Q.CardServices(r.Context(), db.CardServicesParams{WorkspaceID: ws, CardID: &id})
		if err != nil {
			return err
		}
		all := append(append(existing, services...), files...)
		if err := checkStudyFileLimits(all); err != nil {
			return err
		}
		if err := linkStudyFiles(r, tx, h.Blobs, c, files); err != nil {
			return err
		}
		if err := tx.Emit(r.Context(), "study.files_attached", &id, store.User(user), map[string]any{"count": len(files)}); err != nil {
			return err
		}
		view, err = viewOf(r, tx, c)
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 200, view)
	}
}
