package handler

import (
	"testing"

	"lykeion/server/internal/pack"
)

type tChartAgent struct {
	ID, Name, Emoji, Role, Source, Status string
	ReportsTo                             string `json:"reports_to"`
	Since                                 *string
	Studies                               []struct{ ID, Title string }
	Machine, CLI                          *string
}
type tChartJSON struct {
	Rooms []struct {
		Stage, Team string
		Agents      []tChartAgent
	}
	People []struct {
		UserID        string `json:"user_id"`
		Name, Role    string
		OpenDecisions int `json:"open_decisions"`
	}
	Ticker *struct{ At, Text string }
}

func (s *server) labWorkspace(t *testing.T) (string, ws) {
	t.Helper()
	s.H.Shipped = pack.Shipped()
	a := s.signIn(t, "a@chart.org")
	return a, s.workspace(t, a, "chart-one")
}

func (s *server) chart(t *testing.T, tok, wsID string) tChartJSON {
	t.Helper()
	var c tChartJSON
	if code := s.call(t, "GET", "/api/lab-chart", tok, wsID, nil, &c); code != 200 {
		t.Fatalf("lab-chart: %d", code)
	}
	return c
}

func (c tChartJSON) find(id string) (stage string, a tChartAgent, ok bool) {
	for _, r := range c.Rooms {
		for _, x := range r.Agents {
			if x.ID == id {
				return r.Stage, x, true
			}
		}
	}
	return "", tChartAgent{}, false
}

// labAgent is the laboratory agent with the Team member name in a stage.
func (s *server) labAgent(t *testing.T, wsID, stage, member string) (id, name string) {
	t.Helper()
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT a.id, a.name FROM agent a JOIN team_agent t ON t.agent_id = a.id
		WHERE a.workspace_id = $1 AND a.stage = $2 AND t.member = $3 AND t.user_id IS NULL`, wsID, stage, member).Scan(&id, &name); err != nil {
		t.Fatal(err)
	}
	return
}

// roleRun records a role run for an agent on a card.
func (s *server) roleRun(t *testing.T, wsID, cardID, owner, agent, column, status string) {
	t.Helper()
	s.mustUpdate(t, `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, agent_id, started_at)
		VALUES ($1, $2, $3, $4, 1, 'worker', 'claude', 'p', $5, $6, now())`, wsID, cardID, s.userID(t, owner), column, status, agent)
}

func TestLabChartRoomsAndRoles(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	c := s.chart(t, a, w.ID)
	stages, sizes := []string{"grounding", "hypotheses", "experimentation", "validation"}, []int{4, 4, 5, 5}
	if len(c.Rooms) != 4 {
		t.Fatalf("%d rooms", len(c.Rooms))
	}
	for i, r := range c.Rooms {
		roles := map[string]int{}
		for _, x := range r.Agents {
			roles[x.Role]++
			if x.Status != "idle" || x.Source != "laboratory" {
				t.Fatalf("%s: %+v", x.Name, x)
			}
		}
		if r.Stage != stages[i] || len(r.Agents) != sizes[i] || roles["leader"] != 1 || roles["auditor"] != 1 {
			t.Fatalf("room %d: %s %d agents %v", i, r.Stage, len(r.Agents), roles)
		}
	}
}

func TestLabChartPlacesResearcherAgentByStage(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	staged := s.stagedAgent(t, a, w.ID, "experimentation")
	s.mustUpdate(t, `UPDATE agent SET avatar_url = 'emoji:🧪' WHERE id = $1`, staged)
	unstaged := s.stagedAgent(t, a, w.ID, "")
	c := s.chart(t, a, w.ID)
	stage, x, ok := c.find(staged)
	if !ok || stage != "experimentation" || x.Source != "researcher" || x.Role != "member" || x.Emoji != "🧪" || x.ReportsTo != "Experimentation leader" {
		t.Fatalf("%v %s %+v", ok, stage, x)
	}
	if _, _, ok := c.find(unstaged); ok {
		t.Fatal("unstaged agent is on the chart")
	}
}

func TestLabChartWorkingAndStudies(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	id, _ := s.labAgent(t, w.ID, "experimentation", "leader")
	c1, c2 := s.idleCard(t, a, w.ID, "first"), s.idleCard(t, a, w.ID, "second")
	s.roleRun(t, w.ID, c1.ID, a, id, "experimentation", "running")
	_, x, _ := s.chart(t, a, w.ID).find(id)
	if x.Status != "working" || len(x.Studies) != 1 || x.Studies[0].Title != "first" || x.Since == nil {
		t.Fatalf("%+v", x)
	}
	s.roleRun(t, w.ID, c2.ID, a, id, "experimentation", "dispatched")
	if _, x, _ = s.chart(t, a, w.ID).find(id); len(x.Studies) != 2 {
		t.Fatalf("%+v", x)
	}
}

func TestLabChartNeedsYou(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	other := s.member(t, a, w.ID, "m@chart.org", "member")
	id, _ := s.labAgent(t, w.ID, "validation", "auditor")
	c := s.idleCard(t, a, w.ID, "needs")
	s.roleRun(t, w.ID, c.ID, a, id, "validation", "running")
	s.mustUpdate(t, `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,decision_kind,expires_at)
		VALUES($1,$2,$3,'agent_decision','Scope','[]','{"questions":[{"id":"q","prompt":"P","help_text":null,"required":true,"mode":"text","options":[],"allow_other":false}]}','agent','scope',now()+interval '1 day')`, w.ID, c.ID, s.userID(t, a))
	if _, x, _ := s.chart(t, a, w.ID).find(id); x.Status != "needs_you" {
		t.Fatalf("owner sees %s", x.Status)
	}
	if _, x, _ := s.chart(t, other, w.ID).find(id); x.Status != "working" {
		t.Fatalf("colleague sees %s", x.Status)
	}
}

func TestLabChartHidesColleaguesPrivateAgents(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	b := s.member(t, a, w.ID, "b@chart.org", "member")
	priv := s.stagedAgent(t, b, w.ID, "grounding")
	s.mustUpdate(t, `UPDATE agent SET visibility = 'private' WHERE id = $1`, priv)
	c := s.member(t, a, w.ID, "c@chart.org", "member")
	if _, _, ok := s.chart(t, c, w.ID).find(priv); ok {
		t.Fatal("colleague sees B's private agent")
	}
	if _, _, ok := s.chart(t, b, w.ID).find(priv); !ok {
		t.Fatal("B does not see own agent")
	}
}

func TestLabChartPeople(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	s.member(t, a, w.ID, "p@chart.org", "member")
	c := s.chart(t, a, w.ID)
	if len(c.People) != 2 || c.People[0].Role != "owner" || c.People[1].Role != "member" {
		t.Fatalf("%+v", c.People)
	}
}

func TestLabChartTicker(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	if c := s.chart(t, a, w.ID); c.Ticker != nil {
		t.Fatalf("empty workspace ticker %+v", c.Ticker)
	}
	id, name := s.labAgent(t, w.ID, "validation", "auditor")
	c := s.idleCard(t, a, w.ID, "the study")
	s.roleRun(t, w.ID, c.ID, a, id, "validation", "running")
	if tk := s.chart(t, a, w.ID).Ticker; tk == nil || tk.Text != name+" started Validation on “the study”" {
		t.Fatalf("%+v", tk)
	}
	s.mustUpdate(t, `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,decision_kind,expires_at)
		VALUES($1,$2,$3,'agent_decision','Scope','[]','{"questions":[{"id":"q","prompt":"P","help_text":null,"required":true,"mode":"text","options":[],"allow_other":false}]}','agent','scope',now()+interval '1 day')`, w.ID, c.ID, s.userID(t, a))
	if tk := s.chart(t, a, w.ID).Ticker; tk == nil || tk.Text != name+" is waiting for your answer on “the study”" {
		t.Fatalf("%+v", tk)
	}
}

func TestLabChartDecisionWithoutRunDoesNotFail(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	c := s.idleCard(t, a, w.ID, "no run")
	s.mustUpdate(t, `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,decision_kind,expires_at)
		VALUES($1,$2,$3,'agent_decision','Scope','[]','{"questions":[{"id":"q","prompt":"P","help_text":null,"required":true,"mode":"text","options":[],"allow_other":false}]}','agent','scope',now()+interval '1 day')`, w.ID, c.ID, s.userID(t, a))
	got := s.chart(t, a, w.ID)
	for _, r := range got.Rooms {
		for _, x := range r.Agents {
			if x.Status != "idle" {
				t.Fatalf("%s is %s", x.Name, x.Status)
			}
		}
	}
	if got.Ticker != nil {
		t.Fatalf("%+v", got.Ticker)
	}
}

func TestLabChartIgnoresOptionlessDecisions(t *testing.T) {
	s := newServer(t)
	a, w := s.labWorkspace(t)
	id, _ := s.labAgent(t, w.ID, "validation", "auditor")
	c := s.idleCard(t, a, w.ID, "bare")
	s.roleRun(t, w.ID, c.ID, a, id, "validation", "running")
	s.mustUpdate(t, `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,raised_by,decision_kind) VALUES($1,$2,$3,'agent_decision','x','[]','agent','scope')`, w.ID, c.ID, s.userID(t, a))
	if _, x, _ := s.chart(t, a, w.ID).find(id); x.Status != "working" {
		t.Fatalf("%s", x.Status)
	}
}
