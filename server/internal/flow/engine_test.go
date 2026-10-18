package flow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type lab struct {
	env   *testutil.Env
	st    *store.Store
	e     *Engine
	ws    uuid.UUID
	owner uuid.UUID
	pack  db.FieldPack
	man   pack.Manifest
	team  map[string]uuid.UUID // member name → the owner's agent for it
}

func (l *lab) in(t *testing.T, fn func(tx *store.Tx) error) {
	t.Helper()
	if err := l.st.InWorkspace(context.Background(), l.ws, l.owner, fn); err != nil {
		t.Fatal(err)
	}
}

// newLab is a workspace whose owner has added a Team with two members.
func newLab(t *testing.T) *lab {
	t.Helper()
	ctx := context.Background()
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	l := &lab{env: env, st: st, ws: uuid.New(), team: map[string]uuid.UUID{}}
	l.e = &Engine{Store: st, AgentRuns: &service.AgentRuns{Store: st}, Inline: true}
	u, err := st.Global().UpsertUserByEmail(ctx, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	l.owner = u.ID
	p, err := pack.Parse(testutil.Pack(t, testutil.PackOpts{Name: "hyp", Stage: "hypotheses", Members: 2}))
	if err != nil {
		t.Fatal(err)
	}
	l.man = p.Manifest
	manifest, _ := json.Marshal(p.Manifest)
	l.in(t, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: l.ws, Slug: "lab", Name: "Lab"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: l.ws, UserID: l.owner, Role: "owner"}); err != nil {
			return err
		}
		if l.pack, err = tx.Q.InsertFieldPack(ctx, db.InsertFieldPackParams{WorkspaceID: l.ws, Name: p.Manifest.Name, Version: p.Manifest.Version,
			Sha256: p.SHA256, Manifest: manifest, BlobKey: "k", Source: "shipped", Stage: p.Manifest.Stage}); err != nil {
			return err
		}
		rows, err := team.Add(ctx, tx, l.pack, l.man, l.owner)
		for _, r := range rows {
			l.team[r.Member] = r.AgentID
		}
		return err
	})
	return l
}

// study creates a working study in column, worked by the lab's Team when
// teamed, or else by agent.
func (l *lab) study(t *testing.T, column string, teamed bool, agent *uuid.UUID) db.Card {
	t.Helper()
	ctx := context.Background()
	typ := "agent"
	if teamed {
		typ = "team"
	}
	var c db.Card
	l.in(t, func(tx *store.Tx) (err error) {
		n, err := tx.Q.NextCardNumber(ctx, l.ws)
		if err != nil {
			return err
		}
		if c, err = tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: l.ws, Number: n, Title: "t", Question: "Why do cells age?", OwnerID: l.owner,
			FollowsFrom: []uuid.UUID{}, ScopeFrom: column, ScopeTo: column, AssigneeType: &typ, AssigneeID: agent, BoardColumn: column, Round: 1}); err != nil {
			return err
		}
		if teamed {
			return tx.Q.InsertCardTeam(ctx, db.InsertCardTeamParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: column, PackID: l.pack.ID})
		}
		return nil
	})
	return c
}

func (l *lab) agent(t *testing.T, name, harness, model, instructions string) uuid.UUID {
	t.Helper()
	var a db.Agent
	l.in(t, func(tx *store.Tx) (err error) {
		a, err = tx.Q.CreateAgent(context.Background(), db.CreateAgentParams{WorkspaceID: l.ws, Name: name, Instructions: instructions,
			Harness: harness, Model: model, Visibility: "workspace", OwnerID: l.owner})
		return err
	})
	return a.ID
}

type queued struct {
	Role, Harness, Model, Skill, Prompt, Column, Scope string
}

func (l *lab) runs(t *testing.T, card uuid.UUID) []queued {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(), `SELECT role, harness, requested_model, skill, prompt, board_column, continuity_scope
		FROM agent_run WHERE card_id = $1 AND status = 'queued' ORDER BY created_at`, card)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []queued
	for rows.Next() {
		var q queued
		if err := rows.Scan(&q.Role, &q.Harness, &q.Model, &q.Skill, &q.Prompt, &q.Column, &q.Scope); err != nil {
			t.Fatal(err)
		}
		out = append(out, q)
	}
	return out
}

func TestATeamColumnWakesOnlyTheLeader(t *testing.T) {
	l := newLab(t)
	// The run follows the researcher's copy of the leader, not the manifest.
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE agent SET harness = 'codex', model = 'gpt-5.5' WHERE id = $1", l.team["pi"]); err != nil {
		t.Fatal(err)
	}
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	l.e.Kick(l.ws, c.ID) // a second kick must not start the column again
	runs := l.runs(t, c.ID)
	if len(runs) != 1 {
		t.Fatalf("%d queued runs: %+v", len(runs), runs)
	}
	r := runs[0]
	if r.Role != "leader" || r.Harness != "codex" || r.Model != "gpt-5.5" || r.Column != "hypotheses" {
		t.Fatalf("%+v", r)
	}
	if r.Scope != ContinuityScope(c.ID, "leader", "hypotheses", 0) {
		t.Fatalf("continuity scope %q", r.Scope)
	}
	if !strings.Contains(r.Prompt, "Why do cells age?") {
		t.Fatalf("prompt lacks the study's text:\n%s", r.Prompt)
	}
}

func TestANoTeamColumnRunsTheAssignedAgent(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "gpt-5.5", "You are Curie.")
	c := l.study(t, "grounding", false, &a)
	l.e.Kick(l.ws, c.ID)
	runs := l.runs(t, c.ID)
	if len(runs) != 1 || runs[0].Role != "agent" || runs[0].Harness != "codex" || runs[0].Model != "gpt-5.5" || runs[0].Column != "grounding" {
		t.Fatalf("%+v", runs)
	}
}

func TestTheAgentsInstructionsAreTheSkill(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie, a careful reader.")
	c := l.study(t, "grounding", false, &a)
	if err := l.st.InWorkspace(context.Background(), l.ws, uuid.Nil, func(tx *store.Tx) error {
		return l.e.Start(context.Background(), tx, c)
	}); err != nil {
		t.Fatal(err)
	}
	runs := l.runs(t, c.ID)
	if len(runs) != 1 || !strings.HasPrefix(runs[0].Skill, "You are Curie, a careful reader.") {
		t.Fatalf("%+v", runs)
	}
	if !strings.Contains(runs[0].Skill, "lyk team done") || !strings.Contains(runs[0].Skill, "lyk record put") {
		t.Fatalf("the skill lacks the protocol:\n%s", runs[0].Skill)
	}
}

func TestTheLeaderSkillNamesEveryMemberAndItsRole(t *testing.T) {
	m := &pack.Manifest{Leader: pack.Member{Name: "pi", Role: "Leads."}, Members: []pack.Member{
		{Name: "bioinformatician", Role: "Aligns sequences and wrangles structure files."},
		{Name: "data-specialist", Role: "Queries NCBI, UniProt and ClinVar."},
	}}
	s := Protocol("leader", "grounding", m)
	for _, want := range []string{"bioinformatician", "Aligns sequences and wrangles structure files.", "data-specialist",
		"Queries NCBI, UniProt and ClinVar.", "lyk team step", "lyk team done", "--self"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if member := Protocol("bioinformatician", "grounding", m); strings.Contains(member, "lyk team step") || !strings.Contains(member, "lyk record put") || !strings.Contains(member, "--cites") {
		t.Errorf("member protocol:\n%s", member)
	}
}

func TestTheProtocolNamesTheColumnsDeliverable(t *testing.T) {
	m := &pack.Manifest{Leader: pack.Member{Name: "pi"}, Members: []pack.Member{{Name: "x", Role: "Does x."}}}
	const want = "Hypotheses with predictions, rival explanations and what would falsify each"
	for _, role := range []string{"leader", "x", "agent"} {
		mm := m
		if role == "agent" {
			mm = nil
		}
		if s := Protocol(role, "hypotheses", mm); !strings.Contains(s, want) {
			t.Errorf("%s: missing the deliverable in:\n%s", role, s)
		}
	}
	if s := Protocol("agent", "grounding", nil); strings.Contains(s, want) {
		t.Errorf("Grounding names the Hypotheses deliverable:\n%s", s)
	}
}

func TestEveryRoleIsToldAboutLykTools(t *testing.T) {
	m := &pack.Manifest{Leader: pack.Member{Name: "pi"}, Members: []pack.Member{{Name: "x", Role: "Does x."}}}
	for _, role := range []string{"leader", "x", "agent", "auditor"} {
		if s := Protocol(role, "grounding", m); !strings.Contains(s, "lyk tools \"what you need\"") || !strings.Contains(s, "call_tool") {
			t.Errorf("%s is not told about lyk tools:\n%s", role, s)
		}
	}
}

func TestOnlyWorkingStudiesAreCandidates(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	working := l.study(t, "grounding", false, &a)
	want := map[uuid.UUID]bool{working.ID: true}
	for _, set := range []string{"state = 'done'", "state = 'blocked', block_kind = 'low_score'", "board_column = 'backlog', round = 0"} {
		c := l.study(t, "grounding", false, &a)
		if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET "+set+" WHERE id = $1", c.ID); err != nil {
			t.Fatal(err)
		}
		want[c.ID] = false
	}
	cands, err := l.st.Global().BoardCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]bool{}
	for _, c := range cands {
		got[c.CardID] = true
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("card %s a candidate: %v, want %v", id, got[id], w)
		}
	}
}
