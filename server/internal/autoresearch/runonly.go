package autoresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/addon"
	"lykeion/server/internal/board"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/skills"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Role names an Autoresearch agent run's role, its harness setting
// (harness.roles.autoresearch) and its skills folder.
const Role = "autoresearch"

// maxSnapshot bounds the board snapshot in the prompt.
const maxSnapshot = 100

// runOnly queues the run's one agent run on the acting user's runtime. A user with
// no online runtime for the harness is skipped, so a sleeping laptop does not
// pile up runs. The agent run gets the Grounding add-on's servers beside sources,
// but not its text, which is written for the Grounding roles.
func (s *Service) runOnly(ctx context.Context, tx *store.Tx, ap db.Autoresearch, r db.AutoresearchRun) error {
	end := func(status, reason string) error {
		r.Status, r.Reason = status, reason
		return s.setRun(ctx, tx, r)
	}
	w, err := tx.Q.GetWorkspace(ctx, tx.WS)
	if err != nil {
		return err
	}
	harness, model, instructions := flow.ParseSettings(w.Settings).HarnessFor(Role), "", ""
	if ap.AssigneeType != nil {
		a, err := s.assignedAgent(ctx, tx, ap, r.ActingUserID)
		if err != nil {
			return end("failed", "assignee_unavailable")
		}
		harness, model, instructions = a.Harness, a.Model, a.Instructions
	}
	// The agent run always carries the sources server, which the Antigravity adapter
	// refuses before it starts.
	if harness == "antigravity" {
		return end("failed", "harness_unsupported")
	}
	online, err := tx.Q.HasOnlineRuntime(ctx, db.HasOnlineRuntimeParams{WorkspaceID: tx.WS, OwnerID: r.ActingUserID, Harness: harness})
	if err != nil {
		return err
	}
	if !online {
		return end("skipped", "runtime_offline")
	}
	lab, err := addon.Load(ctx, tx.Q, tx.WS, board.Grounding)
	if errors.Is(err, addon.ErrInvalid) {
		return end("failed", "addon_invalid")
	}
	if err != nil {
		return err
	}
	cards, err := tx.Q.ListCards(ctx, tx.WS)
	if err != nil {
		return err
	}
	u, err := tx.Q.GetUser(ctx, r.ActingUserID)
	if err != nil {
		return err
	}
	skill := skills.Role(Role, "run-only")
	if strings.TrimSpace(instructions) != "" {
		skill = strings.TrimSpace(instructions) + "\n\n" + skill
	}
	// Only a webhook run's prompt carries outside data; that run is isolated.
	t, err := s.AgentRuns.EnqueueAutoresearch(ctx, tx, r.ActingUserID, service.AutoresearchAgentRun{Harness: harness, Model: model,
		Prompt: prompt(ap, r, u.Email, cards), Skill: skill,
		RunPrivate: r.Source == "webhook"})
	if err != nil {
		return err
	}
	if h := lab.Hash(); h != "" {
		if err := tx.Q.SetAgentRunAddon(ctx, db.SetAgentRunAddonParams{AddonHash: h, AddonMcp: addon.Canonical(lab.MCP),
			ID: t.ID, WorkspaceID: tx.WS}); err != nil {
			return err
		}
	}
	r.Status, r.Reason, r.AgentRunID = "running", "", &t.ID
	return s.setRun(ctx, tx, r)
}

// assignedAgent is the agent a run of the Autoresearch works as: its assigned
// agent, or the leader of its Team on the first column (the Team set's agent
// where no Team covers that column). A Team's leader is the acting user's own
// copy of that Team's leader agent.
func (s *Service) assignedAgent(ctx context.Context, tx *store.Tx, ap db.Autoresearch, actor uuid.UUID) (db.Agent, error) {
	teams, err := tx.Q.AutoresearchTeams(ctx, db.AutoresearchTeamsParams{WorkspaceID: tx.WS, AutoresearchID: ap.ID})
	if err != nil {
		return db.Agent{}, err
	}
	packID := uuid.Nil
	for _, t := range teams {
		if t.BoardColumn == ap.ScopeFrom {
			packID = t.PackID
		}
	}
	if packID == uuid.Nil {
		if ap.AssigneeID == nil {
			return db.Agent{}, errNoAssignee
		}
		a, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: tx.WS, ID: *ap.AssigneeID})
		if err == nil && a.ArchivedAt != nil {
			err = errNoAssignee
		}
		return a, err
	}
	row, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: tx.WS, ID: packID})
	if err != nil {
		return db.Agent{}, err
	}
	var m pack.Manifest
	if err := json.Unmarshal(row.Manifest, &m); err != nil {
		return db.Agent{}, err
	}
	return tx.Q.GetTeamAgent(ctx, db.GetTeamAgentParams{WorkspaceID: tx.WS, PackID: packID, UserID: actor, Member: m.Leader.Name})
}

var errNoAssignee = errors.New("the assigned agent is unavailable")

// prompt is a run_only agent run's per-run context. Every member sees every card,
// so the board snapshot is the workspace's active cards.
func prompt(ap db.Autoresearch, r db.AutoresearchRun, actor string, cards []db.ListCardsRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the autoresearch role: Lykeion Autoresearch run %s, in run-only mode.\n\n", r.ID)
	fmt.Fprintf(&b, "Autoresearch: %s\nTrigger source: %s\n\nWhat this Autoresearch asks for:\n", ap.Title, r.Source)
	if strings.TrimSpace(ap.Description) != "" {
		b.WriteString(ap.Description)
	} else {
		b.WriteString(ap.Title)
	}
	fmt.Fprintf(&b, "\n\nThe board as %s sees it (context, never evidence):\n", actor)
	n := 0
	for _, c := range cards {
		if c.Ending != "" {
			continue
		}
		if n == maxSnapshot {
			b.WriteString("…and more\n")
			break
		}
		state := c.State
		if c.Flag != "" {
			state = c.Flag
		}
		fmt.Fprintf(&b, "#%d %s %s: %s\n", c.Number, c.BoardColumn, state, c.Title)
		n++
	}
	if n == 0 {
		b.WriteString("(no active cards)\n")
	}
	if r.Source == "webhook" {
		b.WriteString("\n" + fence.Untrusted("The webhook payload that started this run", string(r.Payload)))
	}
	b.WriteString("\nPost your result with `lyk report` (the text on stdin), and open Backlog cards with `lyk open-card`. " +
		"You cannot write records, comment or move cards. Calls through call_tool are logged but are evidence for no card.\n" +
		"To find a skill or database tool for what you need, run `lyk tools \"what you need\"`; call the tools it names through `call_tool`.\n")
	return b.String()
}
