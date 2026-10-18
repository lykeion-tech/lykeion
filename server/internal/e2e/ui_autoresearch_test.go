//go:build unix

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func uiAutoresearchFixtures(mux *http.ServeMux, st *store.Store, auth func(http.ResponseWriter, *http.Request) bool) {
	mux.HandleFunc("POST /lab/autoresearch/{autoresearch}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var in struct {
			WorkspaceID uuid.UUID  `json:"workspace_id"`
			RunID       uuid.UUID  `json:"run_id"`
			Status      string     `json:"status"`
			Reason      string     `json:"reason"`
			Report      string     `json:"report"`
			CardID      *uuid.UUID `json:"card_id"`
			Inbox       bool       `json:"inbox"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		id, err := uuid.Parse(r.PathValue("autoresearch"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var run db.AutoresearchRun
		err = st.InWorkspace(r.Context(), in.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
			ctx := r.Context()
			if _, err := tx.Q.LockWorkspaceExecution(ctx, in.WorkspaceID); err != nil {
				return err
			}
			a, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: in.WorkspaceID, ID: id})
			if err != nil {
				return err
			}
			if in.RunID == uuid.Nil {
				run, err = tx.Q.InsertRun(ctx, db.InsertRunParams{WorkspaceID: in.WorkspaceID, AutoresearchID: id, ActingUserID: a.OwnerID, Source: "manual", PlannedAt: time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC), Status: in.Status, Reason: in.Reason, Payload: json.RawMessage(`{}`), IdempotencyKey: uuid.NewString()})
			} else {
				run, err = tx.Q.LockRun(ctx, db.LockRunParams{WorkspaceID: in.WorkspaceID, ID: in.RunID})
			}
			if err != nil {
				return err
			}
			if run.AutoresearchID != id {
				return fmt.Errorf("run belongs to another autoresearch")
			}
			if err := tx.Q.SetRun(ctx, db.SetRunParams{WorkspaceID: in.WorkspaceID, ID: run.ID, Status: in.Status, Reason: in.Reason, CardID: in.CardID}); err != nil {
				return err
			}
			if err := tx.Q.SetRunReport(ctx, db.SetRunReportParams{WorkspaceID: in.WorkspaceID, ID: run.ID, Report: in.Report}); err != nil {
				return err
			}
			if in.Inbox {
				if err := tx.Q.InsertAutoresearchInboxItem(ctx, db.InsertAutoresearchInboxItemParams{WorkspaceID: in.WorkspaceID, UserID: a.OwnerID, Kind: "autoresearch_report", Text: "Report excerpt", AutoresearchRunID: run.ID, CardID: in.CardID}); err != nil {
					return err
				}
				if err := tx.Emit(ctx, "inbox.created", nil, store.System, map[string]any{}); err != nil {
					return err
				}
			}
			if in.Report != run.Report {
				if err := tx.Emit(ctx, "autoresearch_run.reported", nil, store.System, map[string]any{"run_id": run.ID, "autoresearch_id": id}); err != nil {
					return err
				}
			}
			return tx.Emit(ctx, "autoresearch_run."+in.Status, nil, store.System, map[string]any{"run_id": run.ID, "autoresearch_id": id})
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"run_id": run.ID})
	})
}
