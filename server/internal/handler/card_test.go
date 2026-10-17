package handler

import (
	"io"
	"lykeion/server/internal/testutil"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

type card struct {
	ID          string
	Number      int
	Title       string
	BoardColumn string `json:"board_column"`
	OwnerID     string `json:"owner_id"`
}

func TestCardLifecycleBasics(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c1, c2 card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y in mouse liver?"}, &c1); code != 201 {
		t.Fatalf("create: %d", code)
	}
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"title": "Second", "question": "q2", "follows_from": []string{c1.ID}}, &c2)
	if c1.Number != 1 || c2.Number != 2 || c1.BoardColumn != "backlog" || c1.Title == "" || c1.OwnerID != s.userID(t, a) {
		t.Fatalf("%+v %+v", c1, c2)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": " "}, nil); code != 400 {
		t.Fatalf("empty question: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "follows_from": []string{uuid.NewString()}}, nil); code != 400 {
		t.Fatalf("follows_from an unknown card: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "board_column": "validation"}, nil); code != 400 {
		t.Fatal("a card was created outside Backlog")
	}

	if code := s.call(t, "POST", "/api/cards/"+c1.ID+"/comments", a, w.ID, map[string]string{"body": "Focus on fasting state."}, nil); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	var comments []map[string]any
	s.call(t, "GET", "/api/cards/"+c1.ID+"/comments", a, w.ID, nil, &comments)
	if len(comments) != 1 {
		t.Fatalf("%+v", comments)
	}

	var evs []struct {
		ID   int64
		Type string
	}
	s.call(t, "GET", "/api/cards/"+c1.ID+"/events?after=0", a, w.ID, nil, &evs)
	if len(evs) != 2 || evs[0].Type != "card.created" || evs[1].Type != "comment.created" {
		t.Fatalf("%+v", evs)
	}
	s.call(t, "GET", "/api/cards/"+c1.ID+"/events?after="+itoa(evs[0].ID), a, w.ID, nil, &evs)
	if len(evs) != 1 {
		t.Fatalf("after cursor: %+v", evs)
	}
}

func TestCardsAreScopedToTheirWorkspace(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	var c card
	s.call(t, "POST", "/api/cards", a, wa.ID, map[string]any{"question": "q"}, &c)
	if code := s.call(t, "GET", "/api/cards/"+c.ID, b, wb.ID, nil, nil); code != 404 {
		t.Fatalf("B read A's card through B's workspace: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards", b, wa.ID, nil, nil); code != 404 {
		t.Fatalf("B listed A's workspace: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards", a, "", nil, nil); code != 400 {
		t.Fatalf("missing workspace header: %d", code)
	}
}

func TestInbox(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	var item uuid.UUID
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
		VALUES ($1, $2, $3, 'escalated', '3 failed gates') RETURNING id`, w.ID, c.ID, s.userID(t, a)).Scan(&item); err != nil {
		t.Fatal(err)
	}
	var inbox []map[string]any
	s.call(t, "GET", "/api/inbox", a, w.ID, nil, &inbox)
	if len(inbox) != 1 {
		t.Fatalf("%+v", inbox)
	}
	if code := s.call(t, "POST", "/api/inbox/"+item.String()+"/read", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("read: %d", code)
	}
	if code := s.call(t, "POST", "/api/inbox/"+item.String()+"/read", a, w.ID, nil, nil); code != 404 {
		t.Fatalf("read twice: %d", code)
	}
}

func TestEmptyCollectionResponsesAreJSONArrays(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "empty@lab.org")
	w := s.workspace(t, a, "empty-lab")
	assertBody := func(method, path string) {
		t.Helper()
		req, err := http.NewRequest(method, s.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+a)
		req.Header.Set("X-Workspace-ID", w.ID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || string(body) != "[]\n" {
			t.Errorf("%s %s = status %d body %q, want 200 and []", method, path, resp.StatusCode, body)
		}
	}

	assertBody("GET", "/api/cards")
	assertBody("GET", "/api/inbox")

	var c card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != http.StatusCreated {
		t.Fatalf("create card: %d", code)
	}
	assertBody("GET", "/api/cards/"+c.ID+"/comments")

	var events []struct {
		ID int64
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/events?after=0", a, w.ID, nil, &events); code != http.StatusOK || len(events) == 0 {
		t.Fatalf("read initial events: status %d events %v", code, events)
	}
	assertBody("GET", "/api/cards/"+c.ID+"/events?after="+itoa(events[len(events)-1].ID))
}

type studyJSON struct {
	ID          string
	BoardColumn string `json:"board_column"`
	State       string
	ScopeFrom   string   `json:"scope_from"`
	ScopeTo     string   `json:"scope_to"`
	OffLabel    []string `json:"off_label"`
	Teams       map[string]struct {
		PackID  string `json:"pack_id"`
		Name    string
		Version string
		Stage   string
	}
}

// addedTeam uploads a Team and adds it for the caller, returning its id.
func (s *server) addedTeam(t *testing.T, tok, wsID, name, stage string) string {
	t.Helper()
	var up struct{ ID string }
	if c := s.upload(t, tok, wsID, testutil.Pack(t, testutil.PackOpts{Name: name, Stage: stage}), &up); c != 201 {
		t.Fatalf("upload %s: %d", name, c)
	}
	if c := s.call(t, "POST", "/api/teams/"+up.ID+"/add", tok, wsID, nil, nil); c != 200 {
		t.Fatalf("add %s: %d", name, c)
	}
	return up.ID
}

func teamAssignee(teams map[string]string, extra ...string) map[string]any {
	a := map[string]any{"type": "team", "teams": teams}
	if len(extra) > 0 {
		a["agent_id"] = extra[0]
	}
	return a
}

func TestASingleTeamStudyStaysInItsColumn(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	pack := s.addedTeam(t, a, w.ID, "hyp", "hypotheses")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q",
		"assignee": teamAssignee(map[string]string{"hypotheses": pack})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c.ScopeFrom != "hypotheses" || c.ScopeTo != "hypotheses" || c.BoardColumn != "hypotheses" || c.State != "working" || c.Teams["hypotheses"].PackID != pack {
		t.Fatalf("%+v", c)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q2", "scope": map[string]string{"from": "grounding", "to": "reporting"},
		"assignee": teamAssignee(map[string]string{"hypotheses": pack})}, nil); code != 400 {
		t.Fatalf("a wider scope for one Team: %d", code)
	}
}

func TestATeamSetNeedsAnAgentForItsGaps(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "reporting")
	agent := s.newAgent(t, a, w.ID, "filler", "claude")
	teams := map[string]string{"grounding": g, "reporting": r}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(teams)}, nil); code != 400 {
		t.Fatalf("gaps without an agent: %d", code)
	}
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(teams, agent)}, &c); code != 201 {
		t.Fatalf("with an agent: %d", code)
	}
	if c.ScopeFrom != "grounding" || c.ScopeTo != "reporting" || c.BoardColumn != "grounding" || len(c.Teams) != 2 {
		t.Fatalf("%+v", c)
	}
}

func TestATeamTheCreatorHasNotAddedIsRefused(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var up struct{ ID string }
	if code := s.upload(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "unadded", Stage: "grounding"}), &up); code != 201 {
		t.Fatalf("upload: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": up.ID})}, nil); code != 400 {
		t.Fatalf("unadded Team: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 0 {
		t.Fatalf("%d studies", n)
	}
}

func TestFollowsFromOnlyEndToEnd(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	var parent studyJSON
	single := map[string]any{"question": "parent", "scope": map[string]string{"from": "validation", "to": "validation"}, "assignee": map[string]any{"type": "agent", "id": agent}}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, single, &parent); code != 201 {
		t.Fatalf("parent: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "child 1", "follows_from": []string{parent.ID},
		"scope": map[string]string{"from": "grounding", "to": "hypotheses"}, "assignee": map[string]any{"type": "agent", "id": agent}}, nil); code != 400 {
		t.Fatalf("single-stage follower: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "child 2", "follows_from": []string{parent.ID},
		"assignee": map[string]any{"type": "agent", "id": agent}}, nil); code != 201 {
		t.Fatalf("end-to-end follower: %d", code)
	}
}

func TestUnassignedStudiesWait(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c.BoardColumn != "backlog" || c.ScopeFrom != "grounding" || c.ScopeTo != "reporting" {
		t.Fatalf("%+v", c)
	}
	if n := s.count(t, "SELECT count(*) FROM agent_run"); n != 0 {
		t.Fatalf("%d agent runs", n)
	}
}

func TestALaterColumnsTeamCanChangeUntilItStarts(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r, r2 := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "reporting"), s.addedTeam(t, a, w.ID, "r2", "reporting")
	agent := s.newAgent(t, a, w.ID, "filler", "claude")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "reporting": r}, agent)}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", a, w.ID, map[string]string{"reporting": r2}, nil); code != 200 {
		t.Fatalf("later column: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card_team WHERE card_id = $1 AND board_column = 'reporting' AND pack_id = $2", c.ID, r2); n != 1 {
		t.Fatalf("reporting Team not changed: %d", n)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", a, w.ID, map[string]string{"grounding": r2}, nil); code != 409 {
		t.Fatalf("current column: %d", code)
	}
}

func TestOffLabelColumnsAreMarked(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	pack := s.addedTeam(t, a, w.ID, "hyp", "hypotheses")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": pack})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	var got studyJSON
	s.call(t, "GET", "/api/cards/"+c.ID, a, w.ID, nil, &got)
	if len(got.OffLabel) != 1 || got.OffLabel[0] != "grounding" || got.Teams["grounding"].Stage != "hypotheses" {
		t.Fatalf("%+v", got)
	}
}

func TestDuplicateSaysWhetherFollowingIsAllowed(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question"}, nil); code != 201 {
		t.Fatalf("first: %d", code)
	}
	var dup struct {
		FollowsAllowed bool `json:"follows_allowed"`
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question"}, &dup); code != 409 || !dup.FollowsAllowed {
		t.Fatalf("end-to-end duplicate: %d %+v", code, dup)
	}
	dup.FollowsAllowed = true
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question", "scope": map[string]string{"from": "validation", "to": "validation"},
		"assignee": map[string]any{"type": "agent", "id": agent}}, &dup); code != 409 || dup.FollowsAllowed {
		t.Fatalf("single-stage duplicate: %d %+v", code, dup)
	}
}

func TestOnlyTheOwnerChangesAStudysTeams(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	g, r, r2 := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "reporting"), s.addedTeam(t, a, w.ID, "r2", "reporting")
	agent := s.newAgent(t, a, w.ID, "filler", "claude")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "reporting": r}, agent)}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", b, w.ID, map[string]string{"reporting": r2}, nil); code != 403 {
		t.Fatalf("non-owner: %d", code)
	}
	var up struct{ ID string }
	s.upload(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "r3", Stage: "reporting"}), &up)
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", a, w.ID, map[string]string{"reporting": up.ID}, nil); code != 400 {
		t.Fatalf("Team the owner has not added: %d", code)
	}
}

func TestMixedAssigneeShapesAreRefused(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	pack := s.addedTeam(t, a, w.ID, "g", "grounding")
	for name, assignee := range map[string]map[string]any{
		"agent with teams":     {"type": "agent", "id": agent, "teams": map[string]string{"grounding": pack}},
		"member with agent_id": {"type": "member", "id": s.userID(t, a), "agent_id": agent},
		"team with id":         {"type": "team", "id": agent, "teams": map[string]string{"grounding": pack}},
	} {
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": name, "assignee": assignee}, nil); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
}
