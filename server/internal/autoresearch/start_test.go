package autoresearch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

// stageRuns counts the study's first-round runs in a column, by role.
func (f *fix) stageRuns(t *testing.T, card uuid.UUID, column string) map[string]int {
	t.Helper()
	rows, err := f.env.Owner.Query(context.Background(), `SELECT role FROM agent_run WHERE card_id = $1 AND board_column = $2 AND round = 1`, card, column)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatal(err)
		}
		out[role]++
	}
	return out
}

func (f *fix) agent(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.env.Owner.QueryRow(context.Background(), `INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, $2, 'claude', $3) RETURNING id`,
		f.ws, name, f.owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// addTeam gives user their agents for a Team.
func (f *fix) addTeam(t *testing.T, packID, user uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := f.s.Store.InWorkspace(ctx, f.ws, user, func(tx *store.Tx) error {
		p, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: f.ws, ID: packID})
		if err != nil {
			return err
		}
		var m pack.Manifest
		if err := json.Unmarshal(p.Manifest, &m); err != nil {
			return err
		}
		_, err = team.Add(ctx, tx, p, m, user)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAutoresearchCardStartsAtOnce(t *testing.T) {
	t.Run("an agent", func(t *testing.T) {
		f := setup(t)
		f.s.Engine = &flow.Engine{Store: f.s.Store, AgentRuns: f.s.AgentRuns}
		a := f.agent(t, "digest")
		ap := f.autoresearch(t, "create_card", "Weekly", "What is new?", "")
		f.exec(t, `UPDATE autoresearch SET assignee_type = 'agent', assignee_id = $1, scope_from = 'hypotheses' WHERE id = $2`, a, ap)
		r := f.fire(t, ap, Admission{})
		if r.CardID == nil {
			t.Fatalf("run: %s", reason(r))
		}
		c := f.card(t, *r.CardID)
		if c.BoardColumn != "hypotheses" || c.Round != 1 || c.AssigneeType == nil || *c.AssigneeType != "agent" || *c.AssigneeID != a {
			t.Fatalf("card: %+v", c)
		}
		if got := f.stageRuns(t, c.ID, "hypotheses"); got["agent"] != 1 || len(got) != 1 {
			t.Fatalf("runs: %v", got)
		}
	})
	t.Run("a Team, built-in elsewhere", func(t *testing.T) {
		f := setup(t)
		f.s.Engine = &flow.Engine{Store: f.s.Store, AgentRuns: f.s.AgentRuns}
		hyp := f.builtIn(t, "hypotheses")
		f.addTeam(t, hyp, f.owner)
		ap := f.autoresearch(t, "create_card", "Weekly", "What is new?", "")
		f.exec(t, `UPDATE autoresearch SET assignee_type = 'team', scope_to = 'hypotheses' WHERE id = $1`, ap)
		f.exec(t, `INSERT INTO autoresearch_team (workspace_id, autoresearch_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, f.ws, ap, hyp)
		r := f.fire(t, ap, Admission{})
		if r.CardID == nil {
			t.Fatalf("run: %s", reason(r))
		}
		c := f.card(t, *r.CardID)
		if c.BoardColumn != "grounding" || c.Round != 1 || c.AssigneeType == nil || *c.AssigneeType != "team" {
			t.Fatalf("card: %+v", c)
		}
		if got := f.cardTeams(t, c.ID); len(got) != 2 || got["grounding"] != hyp || got["hypotheses"] != hyp {
			t.Fatalf("teams: %v", got)
		}
		if got := f.stageRuns(t, c.ID, "grounding"); got["leader"] != 1 || len(got) != 1 {
			t.Fatalf("runs: %v", got)
		}
	})
	t.Run("a chosen built-in Team the actor never used", func(t *testing.T) {
		f := setup(t)
		f.s.Engine = &flow.Engine{Store: f.s.Store, AgentRuns: f.s.AgentRuns}
		collab := f.user(t, "c@lab.org", "member")
		hyp := f.builtIn(t, "hypotheses")
		ap := f.autoresearch(t, "create_card", "Weekly", "What is new?", "")
		f.exec(t, `UPDATE autoresearch SET assignee_type = 'team', scope_to = 'hypotheses' WHERE id = $1`, ap)
		f.exec(t, `INSERT INTO autoresearch_team (workspace_id, autoresearch_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, f.ws, ap, hyp)
		r := f.fire(t, ap, Admission{Actor: collab})
		if r.CardID == nil {
			t.Fatalf("run: %s", reason(r))
		}
		if n := f.count(t, `SELECT count(*) FROM team_agent WHERE user_id = $1 AND pack_id = $2`, collab, hyp); n == 0 {
			t.Fatal("the actor has no agents for the chosen built-in Team")
		}
	})
	t.Run("no assignee", func(t *testing.T) {
		f := setup(t)
		f.s.Engine = &flow.Engine{Store: f.s.Store, AgentRuns: f.s.AgentRuns}
		collab := f.user(t, "c@lab.org", "member")
		ap := f.autoresearch(t, "create_card", "Weekly", "What is new?", "")
		r := f.fire(t, ap, Admission{Actor: collab})
		if r.CardID == nil {
			t.Fatalf("run: %s", reason(r))
		}
		c := f.card(t, *r.CardID)
		if c.BoardColumn != "grounding" || c.Round != 1 || c.AssigneeType == nil || *c.AssigneeType != "team" {
			t.Fatalf("card: %+v", c)
		}
		got := f.cardTeams(t, c.ID)
		for _, col := range []string{"grounding", "hypotheses", "experimentation", "validation"} {
			if got[col] != f.builtIn(t, col) {
				t.Fatalf("teams: %v", got)
			}
		}
		if n := f.count(t, `SELECT count(*) FROM team_agent WHERE user_id = $1`, collab); n == 0 {
			t.Fatal("the actor has no agents for the built-in Teams")
		}
		if got := f.stageRuns(t, c.ID, "grounding"); got["leader"] != 1 || len(got) != 1 {
			t.Fatalf("runs: %v", got)
		}
	})
}

func TestAMissingBuiltInFailsTheRunAndLeavesNothing(t *testing.T) {
	f := setup(t)
	f.s.Engine = &flow.Engine{Store: f.s.Store, AgentRuns: f.s.AgentRuns}
	f.exec(t, `DELETE FROM field_pack WHERE id = $1`, f.builtIn(t, "validation"))
	ap := f.autoresearch(t, "create_card", "Weekly", "What is new?", "")
	r := f.fire(t, ap, Admission{})
	if reason(r) != "failed/assignee_unavailable" || r.CardID != nil || f.count(t, `SELECT count(*) FROM card`) != 0 ||
		f.count(t, `SELECT count(*) FROM team_agent`) != 0 {
		t.Fatalf("%s card=%v", reason(r), r.CardID)
	}
}

func TestRunOnlyOpenedStudyRunsItsAgent(t *testing.T) {
	f := setup(t)
	f.s.Engine = &flow.Engine{Store: f.s.Store, AgentRuns: f.s.AgentRuns}
	f.online(t, f.owner, "claude")
	a := f.agent(t, "scout")
	ap := f.autoresearch(t, "run_only", "Scout", "Find new datasets.", "")
	f.exec(t, `UPDATE autoresearch SET assignee_type = 'agent', assignee_id = $1, scope_from = 'experimentation' WHERE id = $2`, a, ap)
	r := f.fire(t, ap, Admission{})
	if r.AgentRunID == nil {
		t.Fatalf("run: %s", reason(r))
	}
	ctx := context.Background()
	var c db.Card
	if err := f.s.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		c, err = f.s.OpenCardForAgentRun(ctx, tx, *r.AgentRunID, "New dataset", "Is it usable?")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if c.BoardColumn != "experimentation" || c.Round != 1 || c.AssigneeType == nil || *c.AssigneeType != "agent" || *c.AssigneeID != a {
		t.Fatalf("card: %+v", c)
	}
	if got := f.stageRuns(t, c.ID, "experimentation"); got["agent"] != 1 || len(got) != 1 {
		t.Fatalf("runs: %v", got)
	}
}
