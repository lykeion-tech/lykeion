package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	case errors.Is(err, flow.ErrStepCap), errors.Is(err, flow.ErrBusy), errors.Is(err, flow.ErrMovedOn), errors.Is(err, flow.ErrDeclaredDone):
		writeErr(w, 409, err.Error())
	case errors.Is(err, flow.ErrPackUnavailable):
		slog.Error("load Team for a step", "card", middleware.RoleFrom(r.Context()).CardID, "err", err)
		writeErr(w, 503, "this study's Team can't be loaded")
	default:
		roleError(w, r, err)
	}
}

// askMember is the leader starting a member's run on a task; its ticket is
// the member's run. While another member works the column it answers busy
// with that member's ticket.
func (h *H) askMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Member string `json:"member"`
		Task   string `json:"task"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Task) == "" {
		writeErr(w, 400, "member and task required")
		return
	}
	if middleware.RoleFrom(r.Context()).Role != "leader" {
		writeErr(w, 403, "only the leader asks members")
		return
	}
	var ticket uuid.UUID
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		_, ticket, err = h.Engine.Delegate(r.Context(), tx, writer(role), in.Member, in.Task)
		return err
	})
	if errors.Is(err, flow.ErrBusy) {
		writeJSON(w, 409, map[string]any{"busy": true, "ticket": ticket, "error": err.Error()})
		return
	}
	if err != nil {
		flowError(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ticket": ticket})
}

// memberResult answers where a member's run the leader asked for stands:
// working while it has not ended, including while it waits to be retried
// after a machine fault, which the leader is never told about; done with its
// hand-in; or failed with its reason.
func (h *H) memberResult(w http.ResponseWriter, r *http.Request) {
	if middleware.RoleFrom(r.Context()).Role != "leader" {
		writeErr(w, 403, "only the leader asks members")
		return
	}
	ticket, err := uuid.Parse(chi.URLParam(r, "ticket"))
	if err != nil {
		writeErr(w, 404, "no such member run")
		return
	}
	var run db.AgentRun
	var handIn json.RawMessage
	var comments []service.SentComment
	err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		run, err = tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: role.WorkspaceID, ID: ticket})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (run.CardID == nil || *run.CardID != role.CardID ||
			run.BoardColumn != role.Column || run.Round != role.Round || !flow.IsMember(run.Role)) {
			return errNoMemberRun
		}
		if err != nil {
			return err
		}
		// The leader is the one listening while its member works: comments
		// posted since its turn started come back to it with the answer.
		leader, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: role.WorkspaceID, ID: role.AgentRunID})
		if err != nil {
			return err
		}
		if comments, err = service.ClaimNewComments(r.Context(), tx, leader, service.DeliveredMemberResult); err != nil || run.Status != "completed" {
			return err
		}
		recs, err := tx.Q.AgentRunRecords(r.Context(), db.AgentRunRecordsParams{WorkspaceID: role.WorkspaceID, AgentRunID: &run.ID})
		for _, rec := range recs {
			var b struct {
				Attempt int32 `json:"attempt"`
			}
			// Only this attempt's hand-in answers: an earlier attempt's was
			// kept with the run but is not what this attempt found.
			if rec.Kind == "hand_in" && rec.SupersededAt == nil && json.Unmarshal(rec.Body, &b) == nil && b.Attempt == run.Attempt {
				handIn = rec.Body
			}
		}
		return err
	})
	if errors.Is(err, errNoMemberRun) {
		writeErr(w, 404, err.Error())
		return
	}
	if err != nil {
		roleError(w, r, err)
		return
	}
	end := time.Now()
	if run.CompletedAt != nil {
		end = *run.CompletedAt
	}
	out := map[string]any{"status": "working", "elapsed_seconds": int(end.Sub(run.CreatedAt).Seconds()), "hand_in": nil, "reason": "", "comments": comments}
	switch {
	case run.Status == "completed" && handIn != nil:
		out["status"], out["hand_in"] = "done", handIn
	case run.Status == "completed":
		out["status"], out["reason"] = "failed", "the member's run ended without a hand-in"
	case run.Status == "cancelled":
		out["status"], out["reason"] = "failed", "the member's run was cancelled"
	case run.Status == "failed":
		reason := strings.TrimSpace(run.Error)
		if reason == "" && run.FailureReason != nil {
			reason = *run.FailureReason
		}
		out["status"], out["reason"] = "failed", reason
	}
	writeJSON(w, 200, out)
}

var errNoMemberRun = errors.New("no such member run")

// listCalls lists the study's recorded database calls in the run's column,
// each answer cut to an excerpt.
func (h *H) listCalls(w http.ResponseWriter, r *http.Request) {
	var recs []db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		recs, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: role.Column})
		if err == nil {
			recs, err = visibleRunRecords(r.Context(), tx, role, recs)
		}
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
		Error         string          `json:"error"`
		WorkspaceCall string          `json:"workspace_call,omitempty"`
	}
	if err := readJSON(r, &in); err != nil || in.Server == "" || in.Tool == "" {
		writeErr(w, 400, "server, tool and answer required")
		return
	}
	if in.Error != "" && (in.Server != "sources" || in.Answer != "" || in.WorkspaceCall != "") {
		writeErr(w, 400, "only a failed sources call may carry an error")
		return
	}
	if len(in.Error) > maxCallAnswer {
		writeErr(w, 400, "an error is at most 32 MiB")
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
	if in.Error != "" {
		body["error"] = in.Error
	}
	hosted := &sources.Hosted{Store: h.Store, Blobs: h.Blobs}
	key, err := hosted.RecordCall(r.Context(), role, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), body, rest, check)
	if errors.Is(err, sources.ErrReserved) {
		writeErr(w, 403, err.Error())
		return
	}
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
		// Notify false looks for tools without telling the owner when none fit.
		Notify *bool `json:"notify"`
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
	var solo *db.FieldPack
	var scope skill.Scope
	var payer uuid.UUID // whose TypeSafe key routes: the study's owner, else who asked for the run
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		run, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{ID: role.AgentRunID, WorkspaceID: role.WorkspaceID})
		if err != nil {
			return err
		}
		if run.CardID != nil {
			card, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: role.WorkspaceID, ID: *run.CardID})
			if err != nil {
				return err
			}
			payer = card.OwnerID
		} else if run.RequesterID != nil {
			payer = *run.RequesterID
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
		if err != nil {
			return err
		}
		if team == nil && run.Role == "agent" && run.Kind == "role" && run.AgentID != nil && run.CardID != nil {
			row, e := tx.ShippedSoloTeam(r.Context(), *run.AgentID, run.BoardColumn)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			if e == nil {
				solo = &row
			}
		}
		return nil
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
	var soloSkills []toolroute.Skill
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
		if solo != nil {
			p, e := pack.Load(r.Context(), h.Blobs, *solo)
			if e != nil {
				fail(w, r, e)
				return
			}
			for _, member := range append([]pack.Member{p.Manifest.Leader}, p.Manifest.Members...) {
				own := toolroute.ForTeam(p, member, nil, nil)
				for _, sk := range own.Skills {
					if !slices.ContainsFunc(soloSkills, func(existing toolroute.Skill) bool { return existing.Name == sk.Name }) {
						soloSkills = append(soloSkills, sk)
					}
				}
			}
			c.Skills = slices.DeleteFunc(c.Skills, func(sk toolroute.Skill) bool {
				return slices.ContainsFunc(soloSkills, func(own toolroute.Skill) bool { return own.Name == sk.Name })
			})
			c.Skills = append(c.Skills, soloSkills...)
		}
	}
	res, err := toolroute.Route(r.Context(), h.Engine.JevFor(r.Context(), payer), in.Need, c)
	if err != nil {
		slog.Warn("route tools", "agent_run", middleware.RoleFrom(r.Context()).AgentRunID, "err", err)
		writeErr(w, 503, err.Error())
		return
	}
	if team == nil {
		names := make([]string, 0, len(res.Skills))
		for _, s := range res.Skills {
			if !slices.ContainsFunc(soloSkills, func(own toolroute.Skill) bool { return own.Name == s.Name }) {
				names = append(names, s.Name)
			}
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
		c.Skills = append(c.Skills, soloSkills...)
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
			if !res.Empty() || in.Notify != nil && !*in.Notify {
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
