package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) putPython(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version string `json:"version"`
	}
	if err := readJSON(r, &in); err != nil || !service.ValidPythonVersion(in.Version) {
		writeErr(w, 400, "version must be a Python version from 3.9 to 3.14")
		return
	}
	var conflict string
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		if role.CardID == uuid.Nil {
			return service.Invalid("this run has no study")
		}
		old, err := service.StudyPython(r.Context(), tx, role.CardID)
		if err != nil {
			return err
		}
		if old != "" {
			if old != in.Version {
				conflict = fmt.Sprintf("This study uses Python %s; a study keeps one Python version.", old)
			}
			return nil
		}
		body, _ := json.Marshal(in)
		rec, err := tx.Q.InsertRecord(r.Context(), db.InsertRecordParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: role.Column, Round: role.Round, Kind: "python_version", Role: "system", Body: body, AgentRunID: &role.AgentRunID})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "record.created", &role.CardID, store.System, map[string]any{"record_id": rec.ID, "kind": "python_version"})
	})
	if err != nil {
		var invalid service.Invalid
		if errors.As(err, &invalid) {
			writeErr(w, 400, err.Error())
		} else {
			roleError(w, r, err)
		}
		return
	}
	if conflict != "" {
		writeErr(w, 409, conflict)
		return
	}
	writeJSON(w, 200, map[string]string{"version": in.Version})
}
