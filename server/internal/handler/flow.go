package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/store"
	"lykeion/server/internal/toolroute"
	"lykeion/server/internal/workspacemcp"
	db "lykeion/server/pkg/db/generated"
)

func writer(role middleware.Role) service.Writer {
	return service.Writer{Card: role.CardID, AgentRun: role.AgentRunID, Column: role.Column, Role: role.Role, Round: role.Round}
}

// flowError answers the engine's refusals of a role command.
func flowError(w http.ResponseWriter, r *http.Request, err error) {
	var inv service.Invalid
	switch {
	case errors.As(err, &inv), errors.Is(err, flow.ErrUndeclaredMember):
		writeErr(w, 400, err.Error())
	case errors.Is(err, flow.ErrStepCap), errors.Is(err, flow.ErrInFlight), errors.Is(err, flow.ErrMovedOn), errors.Is(err, flow.ErrDeclaredDone):
		writeErr(w, 409, err.Error())
	case errors.Is(err, flow.ErrPackUnavailable):
		slog.Error("load Team for a step", "card", middleware.RoleFrom(r.Context()).CardID, "err", err)
		writeErr(w, 503, "this study's Team can't be loaded")
	default:
		roleError(w, r, err)
	}
}

// postStep is the leader naming a member for a step, which starts the
// member's run, or announcing a step it does itself.
func (h *H) postStep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Member  string `json:"member"`
		Content string `json:"content"`
		Self    bool   `json:"self"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Content) == "" {
		writeErr(w, 400, "content required")
		return
	}
	if middleware.RoleFrom(r.Context()).Role != "leader" {
		writeErr(w, 403, "only the leader names members")
		return
	}
	var rec db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		if in.Self {
			rec, err = h.Engine.SelfStep(r.Context(), tx, writer(role), in.Content)
		} else {
			rec, err = h.Engine.Delegate(r.Context(), tx, writer(role), in.Member, in.Content)
		}
		return err
	})
	if err != nil {
		flowError(w, r, err)
		return
	}
	out := map[string]any{"key": rec.Key}
	if !in.Self {
		var body struct {
			AgentRunID string `json:"agent_run_id"`
		}
		json.Unmarshal(rec.Body, &body)
		out["agent_run_id"] = body.AgentRunID
	}
	writeJSON(w, 201, out)
}

// postDone is the leader, or a study's single agent, declaring its column
// done; the column then goes to the auditor.
func (h *H) postDone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Summary string   `json:"summary"`
		Cites   []string `json:"cites"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Summary) == "" {
		writeErr(w, 400, "summary required")
		return
	}
	if role := middleware.RoleFrom(r.Context()).Role; role != "leader" && role != "agent" {
		writeErr(w, 403, "only the leader or the study's agent declares a column done")
		return
	}
	var rec db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		rec, err = h.Engine.Finish(r.Context(), tx, writer(role), in.Summary, in.Cites)
		return err
	})
	if err != nil {
		flowError(w, r, err)
		return
	}
	role := middleware.RoleFrom(r.Context())
	if err := h.Engine.DropWakes(r.Context(), role.WorkspaceID, role.CardID, role.Column, role.Round); err != nil {
		slog.Error("drop leader wakes", "card", role.CardID, "err", err)
	}
	if err := h.Engine.Audit(r.Context(), role.WorkspaceID, role.CardID); err != nil {
		slog.Error("audit", "card", role.CardID, "err", err)
	}
	writeJSON(w, 201, map[string]string{"key": rec.Key})
}

// listCalls lists the study's recorded database calls in the run's column,
// each answer cut to an excerpt.
func (h *H) listCalls(w http.ResponseWriter, r *http.Request) {
	var recs []db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		recs, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: role.Column})
		return err
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	out := []map[string]any{}
	for _, rec := range recs {
		if rec.Kind != "call" {
			continue
		}
		var body map[string]any
		if json.Unmarshal(rec.Body, &body) != nil {
			continue
		}
		if a, ok := body["answer"].(string); ok {
			if cut, more := flow.Excerpt(a, 2000); more {
				body["answer"], body["truncated"] = cut, true
			}
		}
		body["key"] = rec.Key
		out = append(out, body)
	}
	writeJSON(w, 200, out)
}

// maxCallAnswer is the longest answer a posted call may carry.
const maxCallAnswer = 32 << 20

// postCall records a database call the daemon made on the run's behalf. The
// record is built and stored as a catalogue fetch's is.
func (h *H) postCall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Server        string          `json:"server"`
		Tool          string          `json:"tool"`
		Args          json.RawMessage `json:"args"`
		Answer        string          `json:"answer"`
		WorkspaceCall string          `json:"workspace_call,omitempty"`
	}
	if err := readJSON(r, &in); err != nil || in.Server == "" || in.Tool == "" {
		writeErr(w, 400, "server, tool and answer required")
		return
	}
	if len(in.Answer) > maxCallAnswer {
		writeErr(w, 400, "an answer is at most 32 MiB")
		return
	}
	var args any
	if len(in.Args) > 0 {
		args = in.Args
	}
	role := middleware.RoleFrom(r.Context())
	check, err := h.MCPBroker.RecordFence(r.Context(), role, in.WorkspaceCall, in.Server, in.Tool, args, in.Answer)
	if err != nil {
		h.brokerError(w, r, err)
		return
	}
	body, rest := sources.NewCall(role, in.Server, in.Tool, args, in.Answer, false)
	hosted := &sources.Hosted{Store: h.Store, Blobs: h.Blobs}
	key, err := hosted.RecordCall(r.Context(), role, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), body, rest, check)
	if err != nil {
		roleError(w, r, err)
		return
	}
	if key == "" {
		// a run with no card keeps no call record
		writeJSON(w, 201, map[string]string{})
		return
	}
	writeJSON(w, 201, map[string]string{"key": key})
}

// maxNeed bounds the need Jev reads.
const maxNeed = 2000

// postTools asks Jev which of the run's skills and connector tools fit a
// need: a Team run's member's declared ones, or else the workspace's library.
// Jev is asked with no transaction open. A study run leaves a tools record,
// and when nothing fits, tells the study's owner, once while that need's item
// is open; a run with no study only gets the answer.
func (h *H) postTools(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Need           string                      `json:"need"`
		ConnectorTools map[string][]toolroute.Tool `json:"connector_tools"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Need) == "" {
		writeErr(w, 400, "need required")
		return
	}
	if len(in.Need) > maxNeed {
		writeErr(w, 400, fmt.Sprintf("a need is at most %d bytes", maxNeed))
		return
	}
	var conns []pack.Connector
	var team *service.TeamClaim
	var scope skill.Scope
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		run, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{ID: role.AgentRunID, WorkspaceID: role.WorkspaceID})
		if err != nil {
			return err
		}
		if role.CardID != uuid.Nil {
			scope = skill.Scope{Kind: "card", ID: role.CardID}
		} else {
			ar, e := tx.Q.RunByAgentRun(r.Context(), db.RunByAgentRunParams{WorkspaceID: role.WorkspaceID, AgentRunID: role.AgentRunID})
			if e != nil {
				return e
			}
			scope = skill.Scope{Kind: "autoresearch_run", ID: ar.ID}
		}
		conns, team, err = service.ClaimConnectors(r.Context(), tx, run)
		return err
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	brokerCtx := workspacemcp.WithRunAuthority(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	workspace, inventory, checkWorkspace, err := h.MCPBroker.Routing(brokerCtx, middleware.RoleFrom(r.Context()))
	if err != nil {
		h.brokerError(w, r, err)
		return
	}
	if in.ConnectorTools == nil {
		in.ConnectorTools = map[string][]toolroute.Tool{}
	}
	for name, tools := range inventory {
		in.ConnectorTools[name] = tools
	}
	var c toolroute.Candidates
	routeCtx := skill.WithRunAuthority(r.Context(), middleware.RoleFrom(r.Context()), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if team != nil {
		p, member, err := service.TeamMember(r.Context(), h.Blobs, *team)
		if err != nil {
			slog.Error("load Team for tools", "pack", team.Pack.ID, "err", err)
			writeErr(w, 503, "this study's Team can't be loaded")
			return
		}
		member.Connectors = slices.DeleteFunc(member.Connectors, func(ref pack.Connector) bool {
			if ref.Workspace == "" {
				return false
			}
			return !slices.ContainsFunc(workspace, func(c service.WorkspaceConnectorClaim) bool {
				return c.ID.String() == ref.Workspace && c.Name == ref.Name
			})
		})
		c = toolroute.ForTeam(p, member, member.Connectors, in.ConnectorTools)
	} else {
		var library []toolroute.Skill
		routeCtx, library, err = h.skillService().CandidatesForScope(routeCtx, middleware.RoleFrom(r.Context()).WorkspaceID, scope)
		if err != nil {
			roleError(w, r, err)
			return
		}
		for _, wc := range workspace {
			conns = append(conns, pack.Connector{Name: wc.Name, Workspace: wc.ID.String()})
		}
		c = toolroute.ForLibrary(conns, in.ConnectorTools, library)
	}
	res, err := toolroute.Route(r.Context(), h.Engine.Jev, in.Need, c)
	if err != nil {
		slog.Warn("route tools", "agent_run", middleware.RoleFrom(r.Context()).AgentRunID, "err", err)
		writeErr(w, 503, err.Error())
		return
	}
	if team == nil {
		names := make([]string, 0, len(res.Skills))
		for _, s := range res.Skills {
			names = append(names, s.Name)
		}
		c.Skills, err = h.skillService().ResolveForScope(routeCtx, middleware.RoleFrom(r.Context()).WorkspaceID, scope, names)
		if errors.Is(err, skill.ErrRoutingChanged) {
			writeErr(w, 409, err.Error())
			return
		}
		if err != nil {
			roleError(w, r, err)
			return
		}
	}
	if err := c.AttachFolders(&res); err != nil {
		fail(w, r, err)
		return
	}
	if middleware.RoleFrom(r.Context()).CardID == uuid.Nil {
		if err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error { return checkWorkspace(tx) }); err != nil {
			roleError(w, r, err)
			return
		}
	}
	if middleware.RoleFrom(r.Context()).CardID != uuid.Nil {
		err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
			if err := checkWorkspace(tx); err != nil {
				return err
			}
			skills := []string{}
			for _, s := range res.Skills {
				skills = append(skills, s.Name)
			}
			tools := []map[string]string{}
			for _, t := range res.Tools {
				tools = append(tools, map[string]string{"server": t.Server, "tool": t.Tool})
			}
			wr := writer(role)
			wr.Role = "server"
			if _, err := records.WriteAs(r.Context(), tx, wr, "tools", map[string]any{"need": in.Need, "skills": skills, "tools": tools, "judged": res.Judged}); err != nil {
				return err
			}
			if !res.Empty() {
				return nil
			}
			open, err := tx.Q.CountOpenNoTool(r.Context(), db.CountOpenNoToolParams{WorkspaceID: tx.WS, CardID: role.CardID, Need: in.Need})
			if err != nil || open > 0 {
				return err
			}
			card, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: tx.WS, ID: role.CardID})
			if err != nil {
				return err
			}
			return tx.Q.InsertInboxItem(r.Context(), db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: card.ID, UserID: card.OwnerID, Kind: "no_tool",
				Text: noToolText(card.Number, role.Column, in.Need)})
		})
		if err != nil {
			roleError(w, r, err)
			return
		}
	}
	writeJSON(w, 200, res)
}

// noToolText is read back by the dashboard (inboxText in lib/studies.ts), which
// lifts the need out after "what it needed: ". Change them together.
func noToolText(number int32, column, need string) string {
	return fmt.Sprintf("An agent on study #%d's %s column found no skill or connector that fits what it needed: %s", number, column, need)
}
