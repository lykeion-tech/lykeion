package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type researchColleague struct {
	UserID     uuid.UUID         `json:"user_id"`
	Name       string            `json:"name"`
	ProfileURL string            `json:"profile_url"`
	Profile    *researchIdentity `json:"profile"`
}

func (h *H) coresearchResearchDirectory(w http.ResponseWriter, r *http.Request) {
	const maxPageSize = 5
	var limit, offset int64 = 3, 0
	var err error
	query := r.URL.Query()
	if query.Has("limit") {
		limit, err = strconv.ParseInt(query.Get("limit"), 10, 32)
		if err != nil || limit < 1 || limit > maxPageSize {
			writeErr(w, 422, "directory limit must be between 1 and 5")
			return
		}
	}
	if query.Has("offset") {
		offset, err = strconv.ParseInt(query.Get("offset"), 10, 32)
		if err != nil || offset < 0 || offset > math.MaxInt32-maxPageSize {
			writeErr(w, 422, "invalid directory offset")
			return
		}
	}

	ctx := r.Context()
	role := middleware.RoleFrom(ctx)
	var requester uuid.UUID
	// Resolve only the requester as the run. Recheck the same binding under
	// execution, run, token and membership locks before reading as that requester.
	err = h.Store.InWorkspace(ctx, role.WorkspaceID, role.AgentRunID, func(tx *store.Tx) error {
		run, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: role.AgentRunID})
		if err != nil {
			return err
		}
		if run.Kind != "coresearch" || run.RequesterID == nil || run.AgentID == nil {
			return pgx.ErrNoRows
		}
		requester = *run.RequesterID
		return nil
	})
	if !coresearchAnswer(w, r, err) {
		return
	}
	page := struct {
		NextOffset *int32              `json:"next_offset,omitempty"`
		Colleagues []researchColleague `json:"colleagues"`
	}{Colleagues: []researchColleague{}}
	err = h.coresearchRunTxAs(r, false, requester, func(tx *store.Tx, chat db.CoresearchChat, run db.AgentRun) error {
		plain := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		token, err := tx.Q.LockAgentRunToken(ctx, db.LockAgentRunTokenParams{WorkspaceID: tx.WS, AgentRunID: run.ID, TokenHash: auth.Hash(plain)})
		if err != nil {
			return err
		}
		if !slices.Contains(token.Scopes, service.ScopeCoresearch) || run.AgentID == nil {
			return pgx.ErrNoRows
		}
		conversation, err := tx.Q.GetCoresearchConversationForChat(ctx, chat.ID)
		if err != nil {
			return err
		}
		if conversation.AgentID != *run.AgentID || conversation.ProjectID != chat.ProjectID || conversation.SnapshotHarness == nil {
			return pgx.ErrNoRows
		}
		agent, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: tx.WS, ID: conversation.AgentID})
		if err != nil {
			return err
		}
		if agent.BuiltInKey != "aristotle" || agent.OwnerID != requester {
			return pgx.ErrNoRows
		}
		rows, err := tx.Q.ListResearchDirectory(ctx, db.ListResearchDirectoryParams{WorkspaceID: tx.WS, Offset: int32(offset), Limit: int32(limit + 1)})
		if err != nil {
			return err
		}
		// Keep pages small enough for tool output without dropping a colleague.
		size := 0
		for _, row := range rows {
			entry := researchColleague{
				UserID: row.UserID, Name: row.Name,
				ProfileURL: "/colleagues/" + row.UserID.String(),
			}
			if len(row.Profile) > 0 {
				var profile *researchProfile
				if err := json.Unmarshal(row.Profile, &profile); err != nil {
					return err
				}
				if profile != nil {
					entry.Profile = &researchIdentity{Identity: profile.identityText(), Links: profile.Links}
				}
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			if count := len(page.Colleagues); int64(count) == limit || (count > 0 && size+len(raw) > 16<<10) {
				next := int32(offset) + int32(count)
				page.NextOffset = &next
				break
			}
			size += len(raw)
			page.Colleagues = append(page.Colleagues, entry)
		}
		return nil
	})
	if coresearchAnswer(w, r, err) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, page)
	}
}
