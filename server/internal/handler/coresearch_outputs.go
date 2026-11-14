package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Cell output downloads authorize the complete hierarchy before touching storage.
func (h *H) coresearchCellOutputs(w http.ResponseWriter, r *http.Request) {
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if err != nil {
		coresearchAnswer(w, r, pgx.ErrNoRows)
		return
	}
	var cell db.CoresearchCell
	err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		chat, err := coresearchChat(r, tx, ws, user(r), false, false)
		if err != nil {
			return err
		}
		id, err := coresearchID(r, "cell")
		if err != nil {
			return err
		}
		cell, err = tx.Q.GetCoresearchCell(r.Context(), db.GetCoresearchCellParams{WorkspaceID: ws, ID: id})
		if err == nil && cell.ChatID != chat.ID {
			return pgx.ErrNoRows
		}
		return err
	})
	if !coresearchAnswer(w, r, err) {
		return
	}
	raw := []byte(cell.Outputs)
	if cell.OutputsBlob != nil {
		rc, err := h.Blobs.Get(r.Context(), *cell.OutputsBlob)
		if err != nil {
			logoFail(w, r, err)
			return
		}
		defer rc.Close()
		raw, err = io.ReadAll(io.LimitReader(rc, coresearch.MaxCellOutputBytes+1))
		if err == nil && len(bytes.TrimSpace(raw)) == 0 {
			err = errors.New("empty stored cell output blob")
		}
		if err != nil {
			fail(w, r, err)
			return
		}
	}
	raw, err = personCellOutputArray(raw)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// Historical finishes accepted any JSON value. Keep those records readable as
// inert text without changing their stored form or the canonical array wire.
func personCellOutputArray(raw []byte) ([]byte, error) {
	invalid := errors.New("invalid stored cell outputs")
	if len(raw) > coresearch.MaxCellOutputBytes {
		return nil, invalid
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return []byte("[]"), nil
	}
	if !json.Valid(raw) {
		return nil, invalid
	}
	if raw[0] == '[' {
		return raw, nil
	}
	text := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, invalid
		}
	}
	out, err := json.Marshal([]struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: text}})
	if err != nil {
		return nil, err
	}
	if len(out) > coresearch.MaxCellOutputBytes {
		return nil, invalid
	}
	return out, nil
}
