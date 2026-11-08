package handler

import (
	"testing"

	"lykeion/server/internal/testutil"
)

// assignedRun creates an Autoresearch from in, runs it now, and returns the run.
func (s *server) assignedRun(t *testing.T, tok, wsID string, in map[string]any) runView {
	t.Helper()
	ap := s.autoresearch(t, tok, wsID, in)
	var run runView
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/run", tok, wsID, nil, &run); code != 201 {
		t.Fatalf("run: %d", code)
	}
	return run
}

func TestAnAutoresearchNeedsAnAssignee(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card"}, nil); code != 400 {
		t.Fatalf("no assignee: %d", code)
	}
	var up struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "unadded", Stage: "grounding"}), &up)
	for name, as := range map[string]map[string]any{
		"a member":        {"type": "member", "id": s.userID(t, a)},
		"an unadded Team": teamAssignee(map[string]string{"grounding": up.ID}),
	} {
		in := map[string]any{"title": "Digest", "execution_mode": "create_card", "assignee": as}
		if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, in, nil); code != 400 {
			t.Fatalf("%s: %d", name, code)
		}
	}
	if n := s.count(t, "SELECT count(*) FROM autoresearch"); n != 0 {
		t.Fatalf("%d autoresearches", n)
	}
}

func TestAutoresearchHasNoBudget(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "worker", "claude")
	in := map[string]any{"title": "Digest", "execution_mode": "create_card", "budget_usd": 5,
		"assignee": map[string]any{"type": "agent", "id": agent}}
	if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, in, nil); code != 400 {
		t.Fatalf("budget_usd: %d", code)
	}
}

func TestCreateCardStudiesAreAssignedAndStart(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	pack := s.addedTeam(t, a, w.ID, "hyp", "hypotheses")
	run := s.assignedRun(t, a, w.ID, map[string]any{"title": "Weekly", "description": "What is new?", "execution_mode": "create_card",
		"assignee": teamAssignee(map[string]string{"hypotheses": pack})})
	if run.Status != "running" || run.CardID == nil {
		t.Fatalf("run: %+v", run)
	}
	var c studyJSON
	if code := s.call(t, "GET", "/api/cards/"+*run.CardID, a, w.ID, nil, &c); code != 200 {
		t.Fatalf("card: %d", code)
	}
	if c.ScopeFrom != "hypotheses" || c.ScopeTo != "hypotheses" || c.BoardColumn != "hypotheses" || c.State != "working" || c.Teams["hypotheses"].PackID != pack {
		t.Fatalf("%+v", c)
	}
	if n := s.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'leader' AND board_column = 'hypotheses'", *run.CardID); n != 1 {
		t.Fatalf("%d leader runs", n)
	}
}

func TestATeamSetAutoresearchCreatesEndToEndStudies(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "validation")
	agent := s.newAgent(t, a, w.ID, "filler", "claude")
	teams := map[string]string{"grounding": g, "validation": r}
	bad := map[string]any{"title": "Weekly", "execution_mode": "create_card", "assignee": teamAssignee(teams)}
	if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, bad, nil); code != 400 {
		t.Fatalf("gaps without an agent: %d", code)
	}
	run := s.assignedRun(t, a, w.ID, map[string]any{"title": "Weekly", "execution_mode": "create_card", "assignee": teamAssignee(teams, agent)})
	if run.CardID == nil {
		t.Fatalf("run: %+v", run)
	}
	var c studyJSON
	if code := s.call(t, "GET", "/api/cards/"+*run.CardID, a, w.ID, nil, &c); code != 200 {
		t.Fatalf("card: %d", code)
	}
	if c.ScopeFrom != "grounding" || c.ScopeTo != "validation" || c.BoardColumn != "grounding" || len(c.Teams) != 2 {
		t.Fatalf("%+v", c)
	}
}

func TestRunOnlyRunsTheAssignedAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	personal := s.personalToken(t, a)
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d", "harnesses": []string{"codex", "claude"}}, nil); code != 200 {
		t.Fatalf("register: %d", code)
	}
	var ag struct{ ID string }
	if c := s.call(t, "POST", "/api/agents", a, w.ID, map[string]string{"stage": "experimentation", "name": "coder", "harness": "codex", "model": "gpt-x", "instructions": "Be terse."}, &ag); c != 201 {
		t.Fatalf("agent: %d", c)
	}
	run := s.assignedRun(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "run_only", "assignee": map[string]any{"type": "agent", "id": ag.ID}})
	if run.Status != "running" || run.AgentRunID == nil || run.CardID != nil {
		t.Fatalf("run: %+v", run)
	}
	if n := s.count(t, "SELECT count(*) FROM agent_run WHERE id = $1 AND harness = 'codex' AND requested_model = 'gpt-x' AND skill LIKE 'Be terse.%'", *run.AgentRunID); n != 1 {
		t.Fatal("the run did not take the assigned agent's harness, model and instructions")
	}
	// A Team runs its leader, the same way.
	pack := s.addedTeam(t, a, w.ID, "g", "grounding")
	run = s.assignedRun(t, a, w.ID, map[string]any{"title": "Team digest", "execution_mode": "run_only", "assignee": teamAssignee(map[string]string{"grounding": pack})})
	if run.Status != "running" || run.AgentRunID == nil || run.CardID != nil {
		t.Fatalf("team run: %+v", run)
	}
	if n := s.count(t, "SELECT count(*) FROM agent_run WHERE id = $1 AND harness = 'claude' AND skill LIKE 'You are pi.%'", *run.AgentRunID); n != 1 {
		t.Fatal("the Team's run did not use its leader's agent")
	}
}

func TestAnAutoresearchCannotReachPastValidation(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "filler", "claude")
	var e struct{ Error string }
	body := map[string]any{"title": "Weekly", "execution_mode": "create_card", "assignee": map[string]any{"type": "agent", "id": agent},
		"scope": map[string]string{"from": "grounding", "to": "reporting"}}
	if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, body, &e); code != 400 || e.Error != "scope names a column outside the stages" {
		t.Fatalf("scope to reporting: %d %q", code, e.Error)
	}
}
