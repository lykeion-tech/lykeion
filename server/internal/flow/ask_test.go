package flow

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// askBy records run's ask hand-in, with a scope decision from member
// (none when empty), and settles the run as completed.
func (l *lab) askBy(t *testing.T, run db.AgentRun, member string) {
	t.Helper()
	var from any
	if member != "" {
		from = member
	}
	d := map[string]any{"kind": "scope", "title": "Which population?", "body": "The question reads two ways.",
		"options": []any{
			map[string]any{"id": "adults", "label": "Adults", "consequence": "The study looks at adults only."},
			map[string]any{"id": "children", "label": "Children", "consequence": "The study looks at children only."},
		}, "expires_at": nil, "from_member": from}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: *run.CardID, AgentRun: run.ID, Column: run.BoardColumn, Role: run.Role, Round: run.Round},
			"hand_in", map[string]any{"agent_run_id": run.ID, "attempt": 1, "hand_in": map[string]any{"status": "ask", "answer": "I need the researcher.", "reason": nil, "decision": d}})
		return err
	})
	l.settle(t, run, "completed", "", "")
}

func (l *lab) agentDecisions(t *testing.T, card uuid.UUID) int {
	t.Helper()
	return l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'agent_decision'", card)
}

func TestLeadAskBlocksForDecision(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	member := l.man.Members[0].Name
	l.askBy(t, lead, member)
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "decision" || got.Round != c.Round || got.Redos != c.Redos {
		t.Fatalf("after the ask: %s %q round %d redos %d", got.State, got.BlockKind, got.Round, got.Redos)
	}
	var kind, raisedBy, from, dk, def, text string
	var raisedRun uuid.UUID
	var user uuid.UUID
	var days float64
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT kind, raised_by, raised_run_id, from_member, decision_kind, default_option, text, user_id,
		extract(epoch FROM expires_at - created_at) / 86400 FROM inbox_item WHERE card_id = $1 AND kind = 'agent_decision' AND status = 'open'`, c.ID).
		Scan(&kind, &raisedBy, &raisedRun, &from, &dk, &def, &text, &user, &days); err != nil {
		t.Fatal(err)
	}
	if raisedBy != "agent" || raisedRun != lead.ID || from != member || dk != "scope" || def != "" || user != l.owner ||
		!strings.HasPrefix(text, "Which population?") || days < 6.9 || days > 7.1 {
		t.Fatalf("item: %s %s %s %q %s %q %q %s %.2f", kind, raisedBy, raisedRun, from, dk, def, text, user, days)
	}
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
	if n := l.count(t, "SELECT count(*) FROM event WHERE card_id = $1 AND type = 'inbox.created'", c.ID); n != 1 {
		t.Fatalf("%d inbox.created events", n)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("the ask queued %d runs", len(q))
	}
}

// A lead that asks while one of its decisions is open fails its hand-in:
// the column is redone, and no second item is written.
func TestSecondAskWhileOpenFails(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	if _, err := l.env.Owner.Exec(context.Background(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, options, raised_by, decision_kind)
		VALUES ($1, $2, $3, 'agent_decision', 'Earlier question', '[{"id":"a","label":"A","consequence":"a"},{"id":"b","label":"B","consequence":"b"}]', 'agent', 'cost')`,
		l.ws, c.ID, l.owner); err != nil {
		t.Fatal(err)
	}
	l.askBy(t, l.latestRun(t, c.ID, "leader"), "")
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after the second ask: %s redos %d round %d", got.State, got.Redos, got.Round)
	}
	if q := l.runs(t, c.ID); len(q) != 1 || !strings.Contains(q[0].Prompt, "one decision is already open on this study") {
		t.Fatalf("the redo's run: %+v", q)
	}
}

func TestJevSaysDecideYourself(t *testing.T) {
	l := newLab(t)
	s := l.jev(t, 200, levels(nil)) // its choice is the first option by name: decide_yourself
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	asker := l.latestRun(t, c.ID, "agent")
	l.askBy(t, asker, "")
	if n := l.agentDecisions(t, c.ID); n != 0 {
		t.Fatalf("%d agent decisions", n)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.Round != c.Round || got.Redos != c.Redos {
		t.Fatalf("after decide_yourself: %s round %d redos %d", got.State, got.Round, got.Redos)
	}
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "agent" || !strings.Contains(q[0].Prompt, "Decide this yourself: Which population?") || q[0].Scope != asker.ContinuityScope {
		t.Fatalf("the lead's next run: %+v", q)
	}
	calls := s.calls()
	if len(calls) != 1 {
		t.Fatalf("%d Jev calls", len(calls))
	}
	for _, q := range calls[0].Questions {
		if q.Type != "choice" || q.Options["fits"] == "" || q.Options["decide_yourself"] == "" {
			t.Fatalf("Jev was asked %+v", q)
		}
	}
	if !strings.Contains(calls[0].State, "Which population?") || !strings.Contains(calls[0].State, "scope") {
		t.Fatalf("Jev's state: %s", calls[0].State)
	}
}

func TestNoJevSkipsFitCheck(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	l.askBy(t, l.latestRun(t, c.ID, "agent"), "")
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "decision" {
		t.Fatalf("after the ask: %s %q", got.State, got.BlockKind)
	}
}

func TestMemberCannotRaiseDirectly(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	member := l.man.Members[0].Name
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: l.ws, ID: c.ID})
		if err != nil {
			return err
		}
		a, err := teamAgent(ctx, tx, l.pack.ID, l.owner, member)
		if err != nil {
			return err
		}
		_, err = l.e.enqueue(ctx, tx, card, member, &l.man, a, "one")
		return err
	})
	before := len(l.runs(t, c.ID))
	l.askBy(t, l.latestRun(t, c.ID, member), "")
	if n := l.agentDecisions(t, c.ID); n != 0 {
		t.Fatalf("a member's ask raised %d decisions", n)
	}
	if got := l.card(t, c.ID); got.State != "working" {
		t.Fatalf("a member's ask left the study %s", got.State)
	}
	if after := len(l.runs(t, c.ID)); after != before-1 {
		t.Fatalf("a member's ask queued runs: %d before, %d after", before, after)
	}
}

// A lead raises a member's question only for a member of its column's Team;
// a study's single agent has no members to name.
func TestAskFromMemberMustBeOnTheTeam(t *testing.T) {
	ctx := context.Background()
	submit := func(l *lab, c db.Card, from string) []string {
		t.Helper()
		l.e.Kick(l.ws, c.ID)
		ref, run := l.claimOne(t, c.ID, "role")
		raw, _ := json.Marshal(map[string]any{"status": "ask", "answer": "I need the researcher.", "reason": nil, "hypotheses": []any{}, "replies": []any{},
			"decision": map[string]any{"kind": "cost", "title": "Pay for the dataset?", "body": "It costs 400 EUR.", "expires_at": nil, "from_member": from,
				"options": []any{map[string]any{"id": "pay", "label": "Pay", "consequence": "The study buys it."},
					map[string]any{"id": "skip", "label": "Skip", "consequence": "The study uses free data."}}}})
		hs := handin.HandIns{Runs: l.e.AgentRuns}
		probs, err := hs.Submit(ctx, l.owner, ref, run.ID, raw)
		if err != nil {
			t.Fatal(err)
		}
		return probs
	}
	l := newLab(t)
	if p := submit(l, l.study(t, "hypotheses", true, nil), "nobody"); !slices.Contains(p, "decision: from_member must name one of your Team's members, or be null") {
		t.Fatalf("an unknown member: %q", p)
	}
	l = newLab(t)
	if p := submit(l, l.study(t, "hypotheses", true, nil), l.man.Members[1].Name); len(p) != 0 {
		t.Fatalf("a Team member: %q", p)
	}
	l = newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	if p := submit(l, l.study(t, "hypotheses", false, &a), "scout"); !slices.Contains(p, "decision: from_member must be null: you work this column alone") {
		t.Fatalf("a one-agent study: %q", p)
	}
}
