package handler

import (
	"io"
	"lykeion/server/internal/board"
	"lykeion/server/internal/testutil"
	"net/http"
	"strings"
	"testing"
	"time"

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
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c1, c2 card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y in mouse liver?"}, &c1); code != 201 {
		t.Fatalf("create: %d", code)
	}
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"title": "Second", "question": "q2", "follows_from": []string{c1.ID}}, &c2)
	if c1.Number != 1 || c2.Number != 2 || c1.BoardColumn != "grounding" || c1.Title == "" || c1.OwnerID != s.userID(t, a) {
		t.Fatalf("%+v %+v", c1, c2)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": " "}, nil); code != 400 {
		t.Fatalf("empty question: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "follows_from": []string{uuid.NewString()}}, nil); code != 400 {
		t.Fatalf("follows_from an unknown card: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "board_column": "validation"}, nil); code != 400 {
		t.Fatal("a card was created in a column the request named")
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
	s.builtIns(t)
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
	s.builtIns(t)
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
	s.builtIns(t)
	s.H.Engine = nil
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

// addedTeam installs a Team fixture and adds it for the caller, returning its id.
func (s *server) addedTeam(t *testing.T, tok, wsID, name, stage string) string {
	t.Helper()
	var up struct{ ID string }
	s.installPackFixture(t, tok, wsID, testutil.Pack(t, testutil.PackOpts{Name: name, Stage: stage}), &up)
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
	s.builtIns(t)
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
	var wide studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q2", "scope": map[string]string{"from": "grounding", "to": "reporting"},
		"assignee": teamAssignee(map[string]string{"hypotheses": pack})}, &wide); code != 201 {
		t.Fatalf("a wider scope for one Team: %d", code)
	}
	if len(wide.Teams) != 5 || wide.Teams["hypotheses"].PackID != pack || wide.Teams["grounding"].PackID != s.builtInID(t, w.ID, "grounding") {
		t.Fatalf("%+v", wide)
	}
}

func TestATeamSetsGapsGetTheBuiltInTeams(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "reporting")
	teams := map[string]string{"grounding": g, "reporting": r}
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(teams)}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c.ScopeFrom != "grounding" || c.ScopeTo != "reporting" || c.BoardColumn != "grounding" || len(c.Teams) != 5 ||
		c.Teams["grounding"].PackID != g || c.Teams["reporting"].PackID != r || c.Teams["validation"].PackID != s.builtInID(t, w.ID, "validation") {
		t.Fatalf("%+v", c)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "scope": map[string]string{"from": "grounding", "to": "validation"},
		"assignee": teamAssignee(teams)}, nil); code != 400 {
		t.Fatalf("a Team outside the scope: %d", code)
	}
}

func TestATeamTheCreatorHasNotAddedIsRefused(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var up struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "unadded", Stage: "grounding"}), &up)
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": up.ID})}, nil); code != 400 {
		t.Fatalf("unadded Team: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 0 {
		t.Fatalf("%d studies", n)
	}
}

func TestFollowsFromOnlyEndToEnd(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var parent studyJSON
	single := map[string]any{"question": "parent", "scope": map[string]string{"from": "validation", "to": "validation"}}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, single, &parent); code != 201 {
		t.Fatalf("parent: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "child 1", "follows_from": []string{parent.ID},
		"scope": map[string]string{"from": "grounding", "to": "hypotheses"}}, nil); code != 400 {
		t.Fatalf("single-stage follower: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "child 2", "follows_from": []string{parent.ID}}, nil); code != 201 {
		t.Fatalf("end-to-end follower: %d", code)
	}
}

func TestAStudyWithNoTeamPickedStartsAtOnceEndToEnd(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c.BoardColumn != "grounding" || c.ScopeFrom != "grounding" || c.ScopeTo != "reporting" || len(c.Teams) != 5 {
		t.Fatalf("%+v", c)
	}
	for _, col := range board.Stages {
		if c.Teams[col].PackID != s.builtInID(t, w.ID, col) {
			t.Fatalf("%s: %+v", col, c.Teams[col])
		}
	}
	// The engine starts a study after the request's transaction commits.
	if n := s.queuedRuns(t, c.ID); n != 1 || s.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'leader' AND board_column = 'grounding'", c.ID) != 1 {
		t.Fatalf("%d queued runs", n)
	}
}

func TestALaterColumnsTeamCanChangeUntilItStarts(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r, r2 := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "reporting"), s.addedTeam(t, a, w.ID, "r2", "reporting")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "reporting": r})}, &c); code != 201 {
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
	s.builtIns(t)
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
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question"}, nil); code != 201 {
		t.Fatalf("first: %d", code)
	}
	var dup struct {
		Error          string
		FollowsAllowed bool `json:"follows_allowed"`
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question"}, &dup); code != 409 || !dup.FollowsAllowed || dup.Error != "an active study already asks this: #1" {
		t.Fatalf("end-to-end duplicate: %d %+v", code, dup)
	}
	dup.FollowsAllowed = true
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question", "scope": map[string]string{"from": "validation", "to": "validation"}}, &dup); code != 409 || dup.FollowsAllowed {
		t.Fatalf("single-stage duplicate: %d %+v", code, dup)
	}
}

func TestATeamCountsTheStudiesItWorks(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "reporting")
	for _, q := range []string{"one?", "two?"} {
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": q, "assignee": teamAssignee(map[string]string{"grounding": g, "reporting": g})}, nil); code != 201 {
			t.Fatalf("create: %d", code)
		}
	}
	for team, want := range map[string]int{g: 2, r: 0} {
		var rows []packRow
		s.call(t, "GET", "/api/packs?name="+map[string]string{g: "g", r: "r"}[team], a, w.ID, nil, &rows)
		if len(rows) != 1 || rows[0].PinnedStudies != want {
			t.Fatalf("team %s: %+v, want %d studies", team, rows, want)
		}
	}
}

func TestADoneStudyNoLongerHoldsItsQuestion(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var first card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question"}, &first)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding', round = 1, state = 'done' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "same question"}, nil); code != 201 {
		t.Fatalf("a done study still holds its question: %d", code)
	}
}

func TestOnlyTheOwnerChangesAStudysTeams(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	g, r, r2 := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "reporting"), s.addedTeam(t, a, w.ID, "r2", "reporting")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "reporting": r})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", b, w.ID, map[string]string{"reporting": r2}, nil); code != 403 {
		t.Fatalf("non-owner: %d", code)
	}
	var up struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "r3", Stage: "reporting"}), &up)
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", a, w.ID, map[string]string{"reporting": up.ID}, nil); code != 400 {
		t.Fatalf("Team the owner has not added: %d", code)
	}
}

func TestMixedAssigneeShapesAreRefused(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
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

// builtIns ships a free Team for each stage named, or for every stage;
// workspaces made afterwards have them.
func (s *server) builtIns(t *testing.T, stages ...string) {
	t.Helper()
	if len(stages) == 0 {
		stages = board.Stages
	}
	var zips [][]byte
	for _, st := range stages {
		zips = append(zips, testutil.Pack(t, testutil.PackOpts{Name: strings.ToUpper(st[:1]) + st[1:] + " Team", Stage: st}))
	}
	s.H.Shipped = shippedFS(t, zips...)
}

// builtInID is the id of the workspace's built-in Team for a stage.
func (s *server) builtInID(t *testing.T, wsID, stage string) string {
	t.Helper()
	var id string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT id::text FROM field_pack WHERE workspace_id = $1 AND source = 'shipped' AND stage = $2`, wsID, stage).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// queuedRuns waits for the engine to queue a study's first run.
func (s *server) queuedRuns(t *testing.T, cardID string) int {
	t.Helper()
	n := 0
	for deadline := time.Now().Add(5 * time.Second); n == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		n = s.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND status = 'queued'", cardID)
	}
	return n
}

func TestNoTeamPickedUsesTheBuiltInTeams(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "scope": map[string]string{"from": "hypotheses", "to": "hypotheses"}}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c.BoardColumn != "hypotheses" || c.State != "working" || len(c.Teams) != 1 || c.Teams["hypotheses"].PackID != s.builtInID(t, w.ID, "hypotheses") {
		t.Fatalf("%+v", c)
	}
	if n := s.count(t, "SELECT count(*) FROM card WHERE id = $1 AND round = 1 AND assignee_type = 'team'", c.ID); n != 1 {
		t.Fatal("the study did not start on round 1 as a Team study")
	}
	if n := s.queuedRuns(t, c.ID); n != 1 {
		t.Fatalf("%d queued runs", n)
	}
}

func TestAChosenTeamFillsItsColumnAndBuiltInsTheRest(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	exp := s.addedTeam(t, a, w.ID, "exp", "experimentation")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "scope": map[string]string{"from": "hypotheses", "to": "validation"},
		"assignee": teamAssignee(map[string]string{"experimentation": exp})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if len(c.Teams) != 3 || c.Teams["hypotheses"].PackID != s.builtInID(t, w.ID, "hypotheses") ||
		c.Teams["experimentation"].PackID != exp || c.Teams["validation"].PackID != s.builtInID(t, w.ID, "validation") || c.BoardColumn != "hypotheses" {
		t.Fatalf("%+v", c)
	}
}

func TestAStudyCannotBeAssignedToAnAgentOrMember(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	for name, assignee := range map[string]map[string]any{
		"agent":           {"type": "agent", "id": agent},
		"member":          {"type": "member", "id": s.userID(t, a)},
		"team with agent": {"type": "team", "teams": map[string]string{"grounding": s.builtInID(t, w.ID, "grounding")}, "agent_id": agent},
	} {
		var e struct{ Error string }
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": name, "assignee": assignee}, &e); code != 400 || e.Error != "a study is worked by a Team" {
			t.Errorf("%s: %d %q", name, code, e.Error)
		}
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 0 {
		t.Fatalf("%d studies", n)
	}
}

func TestCreateRefusesWhenAStageHasNoBuiltInTeam(t *testing.T) {
	s := newServer(t)
	s.builtIns(t, board.Stages[:4]...)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var e struct{ Error string }
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &e); code != 422 || e.Error != "No built-in Team for Reporting" {
		t.Fatalf("%d %q", code, e.Error)
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 0 {
		t.Fatalf("%d studies", n)
	}
}

func TestAStartedStudyCannotBeEdited(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	for _, body := range []map[string]any{{"title": "t"}, {"question": "q2"}, {"deadline": "2026-12-01T00:00:00Z"}, {"assignee": map[string]any{"type": "team"}}} {
		if code := s.call(t, "PATCH", "/api/cards/"+c.ID, a, w.ID, body, nil); code != 405 {
			t.Fatalf("edit %v: %d", body, code)
		}
	}
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/properties", a, w.ID, map[string]any{"assignee_type": "agent", "assignee_id": agent}, nil); code != 400 {
		t.Fatalf("reassign: %d", code)
	}
	var res struct{ ID string }
	if code := s.call(t, "POST", "/api/researches", a, w.ID, map[string]any{"name": "Ageing"}, &res); code != 201 {
		t.Fatalf("research: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/properties", a, w.ID, map[string]any{"research_id": res.ID}, nil); code != 200 {
		t.Fatalf("research stays editable: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card WHERE id = $1 AND title = 'q' AND assignee_type = 'team' AND assignee_id IS NULL", c.ID); n != 1 {
		t.Fatal("the study changed")
	}
}

func TestTheOwnerDeletesAStudyAnyTime(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if n := s.queuedRuns(t, c.ID); n != 1 {
		t.Fatalf("the study is not working: %d queued runs", n)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+c.ID, b, w.ID, nil, nil); code != 403 {
		t.Fatalf("another member deletes: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+c.ID, a, w.ID, nil, nil); code != 204 {
		t.Fatalf("owner deletes: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card WHERE id = $1", c.ID) + s.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1", c.ID); n != 0 {
		t.Fatalf("%d rows left", n)
	}
}

func TestDeletingAWorkingStudyCancelsItsRunningRun(t *testing.T) {
	l := newTeamLab(t, 0)
	run := l.claim(t)
	if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if n := l.s.count(t, `SELECT count(*) FROM event WHERE type = 'agent_run.cancelled' AND payload->>'agent_run_id' = $1`, run.id); n != 1 {
		t.Fatalf("%d agent_run.cancelled events", n)
	}
	if code := l.s.call(t, "POST", "/api/role/report", run.token, "", map[string]string{"text": "still here"}, nil); code != 401 {
		t.Fatalf("the run's token still works: %d", code)
	}
	status := "/api/daemon/agent-runs/" + run.id + "/status?workspace_id=" + l.ws + "&runtime_id=" + l.rts + "&attempt=1"
	if code := l.s.call(t, "GET", status, l.personal, "", nil, nil); code != 404 {
		t.Fatalf("status of the deleted run: %d", code)
	}
}
