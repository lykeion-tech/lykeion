package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/addon"
	"lykeion/server/internal/board"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type addonView struct {
	BoardColumn string          `json:"board_column"`
	SkillMD     string          `json:"skill_md"`
	MCP         json.RawMessage `json:"mcp"`
	AddonHash   string          `json:"addon_hash"`
	Invalid     string          `json:"invalid,omitempty"` // why a stored row cannot be used; cards in this column escalate
	UpdatedBy   uuid.UUID       `json:"updated_by"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func viewAddon(row db.StageAddon) addonView {
	v := addonView{BoardColumn: row.BoardColumn, SkillMD: row.SkillMd, MCP: row.Mcp, UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt}
	ss, err := addon.Parse(row.Mcp)
	if err == nil {
		err = addon.CheckText(row.SkillMd)
	}
	if err != nil {
		v.Invalid = err.Error()
		return v
	}
	v.AddonHash = addon.Addon{SkillMD: row.SkillMd, MCP: ss}.Hash()
	return v
}

func (h *H) listAddons(w http.ResponseWriter, r *http.Request) {
	var rows []db.StageAddon
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		rows, err = tx.Q.ListStageAddons(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	out := []addonView{}
	for _, row := range rows {
		out = append(out, viewAddon(row))
	}
	writeJSON(w, 200, out)
}

// setAddon changes one column's add-on. A field left out keeps its stored
// value, and the result must pass every add-on rule before it is stored.
// Only skill_md and mcp exist: nothing here reaches the gate.
func (h *H) setAddon(w http.ResponseWriter, r *http.Request) {
	column := chi.URLParam(r, "column")
	if !slices.Contains(board.Stages, column) {
		writeErr(w, 400, "column must be one of "+strings.Join(board.Stages, ", "))
		return
	}
	var in struct {
		SkillMD *string         `json:"skill_md"`
		MCP     json.RawMessage `json:"mcp"`
	}
	if err := readAddonJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid add-on: "+err.Error()+" (only skill_md and mcp can be set)")
		return
	}
	ctx := r.Context()
	var out db.StageAddon
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		cur, err := tx.Q.GetStageAddon(ctx, db.GetStageAddonParams{WorkspaceID: ws, BoardColumn: column})
		if errors.Is(err, pgx.ErrNoRows) {
			cur, err = db.StageAddon{Mcp: json.RawMessage("[]")}, nil
		}
		if err != nil {
			return err
		}
		text, raw := cur.SkillMd, cur.Mcp
		if in.SkillMD != nil {
			text = strings.TrimSpace(*in.SkillMD)
		}
		if in.MCP != nil {
			raw = in.MCP
		}
		if err := addon.CheckText(text); err != nil {
			return err
		}
		ss, err := addon.Parse(raw)
		if err != nil {
			return err
		}
		out, err = tx.Q.UpsertStageAddon(ctx, db.UpsertStageAddonParams{WorkspaceID: ws, BoardColumn: column,
			SkillMd: text, Mcp: addon.Canonical(ss), UpdatedBy: user})
		if err != nil {
			return err
		}
		names := []string{}
		for _, s := range ss {
			names = append(names, s.Name)
		}
		return tx.Emit(ctx, "stage_addon.updated", nil, store.User(user), map[string]any{
			"column": column, "addon_hash": addon.Addon{SkillMD: text, MCP: ss}.Hash(), "servers": names})
	})
	if errors.Is(err, addon.ErrInvalid) {
		writeErr(w, 400, err.Error())
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, viewAddon(out))
}

// readAddonJSON checks the original bytes before JSON decoding can replace
// malformed UTF-8, while retaining the shared decoder's bounds and field rules.
func readAddonJSON(r *http.Request, v any) error {
	limit := middleware.JSONBodyLimit(r)
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("request body exceeds endpoint limit")
	}
	if !utf8.Valid(data) {
		return errors.New("request body is not valid UTF-8")
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return readJSON(r, v)
}
