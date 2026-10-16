package autoresearch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lykeion/server/internal/addon"
	"lykeion/server/internal/board"
	"lykeion/server/internal/round"
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
	harness := round.ParseSettings(w.Settings).HarnessFor(Role)
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
	// Only a webhook run's prompt carries outside data; that run is isolated.
	t, err := s.AgentRuns.EnqueueAutoresearch(ctx, tx, r.ActingUserID, service.AutoresearchAgentRun{Harness: harness,
		Prompt: prompt(ap, r, u.Email, cards), Skill: skills.Role(Role, "run-only"), RunPrivate: r.Source == "webhook"})
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
		b.WriteString("\n" + round.Untrusted("The webhook payload that started this run", string(r.Payload)))
	}
	b.WriteString("\nPost your result with `lyk report` (the text on stdin), and open Backlog cards with `lyk open-card`. " +
		"You cannot write records, comment or move cards. Searches through lyk_sources are logged but are evidence for no card; other tools you have are not logged.\n")
	return b.String()
}
