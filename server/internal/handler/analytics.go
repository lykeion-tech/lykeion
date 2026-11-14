package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/analytics"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) analytics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	scope, span, cadence := query.Get("scope"), query.Get("range"), query.Get("cadence")
	if scope == "" {
		scope = "all"
	}
	if span == "" {
		span = "30d"
	}
	if cadence == "" {
		cadence = "daily"
	}
	var research *uuid.UUID
	if scope != "all" && scope != "standalone" {
		id, err := uuid.Parse(scope)
		if err != nil {
			writeErr(w, 400, "invalid analytics scope")
			return
		}
		research = &id
		scope = id.String()
	}
	if _, _, _, err := analytics.Bounds(time.Now(), "UTC", span, cadence); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var out analytics.Response
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if research != nil {
			if _, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: *research}); err != nil {
				return err
			}
		}
		profile, err := tx.Q.GetUser(r.Context(), user)
		if err != nil {
			return err
		}
		rows, err := tx.Q.AnalyticsRuns(r.Context(), db.AnalyticsRunsParams{WorkspaceID: ws, Scope: scope, ResearchID: research})
		if err != nil {
			return err
		}
		records, err := tx.Q.AnalyticsUsage(r.Context(), db.AnalyticsUsageParams{WorkspaceID: ws, Scope: scope, ResearchID: research})
		if err != nil {
			return err
		}
		history, err := tx.Q.AnalyticsEvents(r.Context(), db.AnalyticsEventsParams{WorkspaceID: ws, Scope: scope, ResearchID: research})
		if err != nil {
			return err
		}
		runs := make([]analytics.Run, 0, len(rows))
		for _, row := range rows {
			if row.CardID != nil {
				runs = append(runs, analytics.Run{ID: row.ID, StudyID: *row.CardID, AgentID: row.AgentID, AgentName: row.AgentName, Role: row.Role, Harness: row.Harness, Status: row.Status, Attempt: row.Attempt, StartedAt: row.StartedAt})
			}
		}
		usage := make([]analytics.Usage, 0, len(records))
		for _, row := range records {
			usage = append(usage, analytics.Usage{RunID: row.AgentRunID, Attempt: row.Attempt, Input: row.InputTokens, Output: row.OutputTokens, CacheRead: row.CacheReadTokens, CacheWrite: row.CacheWriteTokens, Cost: row.CostUsd, Reported: row.CostReported})
		}
		events := make([]analytics.Event, 0, len(history))
		for _, row := range history {
			var payload struct {
				RunID   uuid.UUID `json:"agent_run_id"`
				Attempt int32     `json:"attempt"`
			}
			if json.Unmarshal(row.Payload, &payload) != nil {
				continue
			}
			events = append(events, analytics.Event{RunID: payload.RunID, Attempt: payload.Attempt, Type: row.Type, At: row.CreatedAt})
		}
		out, err = analytics.Aggregate(time.Now().UTC(), profile.Timezone, span, cadence, scope, runs, events, usage)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}
