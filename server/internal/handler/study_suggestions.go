package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
)

func (h *H) listStudySuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	who := user(r)
	rows, err := tx.PrivateStudySuggestions(ctx, who)
	if err != nil {
		fail(w, r, err)
		return
	}
	out := []map[string]any{}
	for _, row := range rows {
		_, _, safe, err := labmemory.NewDisplayReader(tx, who, row.ChatID).Answer(ctx, row)
		if err != nil {
			fail(w, r, err)
			return
		}
		if !safe {
			continue
		}
		var suggestion labmemory.StudySuggestion
		if err := json.Unmarshal(row.Proposal, &suggestion); err != nil {
			fail(w, r, err)
			return
		}
		out = append(out, map[string]any{"id": row.ID, "chat_id": row.ChatID, "description": suggestion.Description, "stage": suggestion.Stage, "assignee": suggestion.Assignee, "assignee_name": suggestion.AssigneeName})
	}
	writeJSON(w, 200, out)
}

func (h *H) validateSuggestion(r *http.Request, tx *store.Tx, user, id uuid.UUID, fromSuggestion bool) error {
	row, err := tx.SuggestionSource(r.Context(), user, id)
	if errors.Is(err, pgx.ErrNoRows) {
		if fromSuggestion {
			return conflict("suggestion unavailable; refresh Up next")
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, _, safe, err := labmemory.NewDisplayReader(tx, user, row.ChatID).Answer(r.Context(), row)
	if err != nil {
		return err
	}
	if !safe {
		return conflict("suggestion evidence changed; refresh Up next")
	}
	return nil
}
