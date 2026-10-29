package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"lykeion/server/internal/board"
	"lykeion/server/internal/testutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/records"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
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
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "Does X raise Y in mouse liver?"}, &c1); code != 201 {
		t.Fatalf("create: %d", code)
	}
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "title": "Second", "question": "q2"}, &c2)
	if c1.Number != 1 || c2.Number != 2 || c1.BoardColumn != "grounding" || c1.Title == "" || c1.OwnerID != s.userID(t, a) {
		t.Fatalf("%+v %+v", c1, c2)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": " "}, nil); code != 400 {
		t.Fatalf("empty question: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "board_column": "validation"}, nil); code != 400 {
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
	s.call(t, "POST", "/api/cards", a, wa.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c)
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
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c)
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
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != http.StatusCreated {
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
		PackID string `json:"pack_id"`
		Name   string
		Stage  string
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
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q2", "scope": map[string]string{"from": "grounding", "to": "validation"},
		"assignee": teamAssignee(map[string]string{"hypotheses": pack})}, &wide); code != 201 {
		t.Fatalf("a wider scope for one Team: %d", code)
	}
	if len(wide.Teams) != 4 || wide.Teams["hypotheses"].PackID != pack || wide.Teams["grounding"].PackID != s.builtInID(t, w.ID, "grounding") {
		t.Fatalf("%+v", wide)
	}
}

func TestATeamSetsGapsGetTheBuiltInTeams(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "validation")
	teams := map[string]string{"grounding": g, "validation": r}
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "assignee": teamAssignee(teams)}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c.ScopeFrom != "grounding" || c.ScopeTo != "validation" || c.BoardColumn != "grounding" || len(c.Teams) != 4 ||
		c.Teams["grounding"].PackID != g || c.Teams["validation"].PackID != r {
		t.Fatalf("%+v", c)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "scope": map[string]string{"from": "grounding", "to": "experimentation"},
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

func TestFollowsFromIsRefused(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "follows_from": []string{}}, nil); code != 400 {
		t.Fatalf("follows_from: %d", code)
	}
}

func TestAStudyWithNoTeamPickedStartsAtOnceEndToEnd(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c.BoardColumn != "grounding" || c.ScopeFrom != "grounding" || c.ScopeTo != "validation" || len(c.Teams) != 4 {
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
	g, r, r2 := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "validation"), s.addedTeam(t, a, w.ID, "r2", "validation")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "validation": r})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", a, w.ID, map[string]string{"validation": r2}, nil); code != 200 {
		t.Fatalf("later column: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card_team WHERE card_id = $1 AND board_column = 'validation' AND pack_id = $2", c.ID, r2); n != 1 {
		t.Fatalf("validation Team not changed: %d", n)
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

func TestADuplicateHasNoFollowUp(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "same question"}, nil); code != 201 {
		t.Fatalf("first: %d", code)
	}
	var dup map[string]any
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "same question"}, &dup); code != 409 {
		t.Fatalf("end-to-end duplicate: %d %+v", code, dup)
	}
	_, hasFollows := dup["follows_allowed"]
	if dup["card_id"] == nil || dup["number"] != float64(1) || hasFollows {
		t.Fatalf("body: %+v", dup)
	}
}

func TestATeamCountsTheStudiesItWorks(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "validation")
	for _, q := range []string{"one?", "two?"} {
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": q, "assignee": teamAssignee(map[string]string{"grounding": g, "validation": g})}, nil); code != 201 {
			t.Fatalf("create: %d", code)
		}
	}
	if c, e := s.saveSkills(t, a, w.ID, g); c != 409 || !strings.HasPrefix(e, "Used by 2 studies.") {
		t.Fatalf("Team two studies use: %d %q", c, e)
	}
	if c, e := s.saveSkills(t, a, w.ID, r); c != 200 {
		t.Fatalf("Team no study uses: %d %q", c, e)
	}
}

func TestADoneStudyNoLongerHoldsItsQuestion(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var first card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "same question"}, &first)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding', round = 1, state = 'done' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "same question"}, nil); code != 201 {
		t.Fatalf("a done study still holds its question: %d", code)
	}
}

func TestOnlyTheOwnerChangesAStudysTeams(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	g, r, r2 := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "validation"), s.addedTeam(t, a, w.ID, "r2", "validation")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "validation": r})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", b, w.ID, map[string]string{"validation": r2}, nil); code != 403 {
		t.Fatalf("non-owner: %d", code)
	}
	var up struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "r3", Stage: "validation"}), &up)
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", a, w.ID, map[string]string{"validation": up.ID}, nil); code != 400 {
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
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "scope": map[string]string{"from": "hypotheses", "to": "validation"},
		"assignee": teamAssignee(map[string]string{"experimentation": exp})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if len(c.Teams) != 3 || c.Teams["hypotheses"].PackID != s.builtInID(t, w.ID, "hypotheses") ||
		c.Teams["experimentation"].PackID != exp || c.Teams["validation"].PackID != s.builtInID(t, w.ID, "validation") || c.BoardColumn != "hypotheses" {
		t.Fatalf("%+v", c)
	}
}

func TestAOneAgentStudyStartsAtOnce(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": map[string]any{"type": "agent", "id": agent},
		"scope": map[string]string{"from": "grounding", "to": "grounding"}}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card WHERE id = $1 AND assignee_type = 'agent' AND assignee_id = $2 AND board_column = 'grounding'", c.ID, agent); n != 1 {
		t.Fatalf("the study is not assigned to the agent in Grounding: %+v", c)
	}
	if n := s.count(t, "SELECT count(*) FROM card_team WHERE card_id = $1", c.ID); n != 0 {
		t.Fatalf("%d Teams on a one-agent study", n)
	}
	if n := s.queuedRuns(t, c.ID); n != 1 {
		t.Fatalf("%d queued runs", n)
	}
	if n := s.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND status = 'queued' AND role = 'agent' AND agent_id = $2", c.ID, agent); n != 1 {
		t.Fatal("the queued run is not the agent's")
	}
	var whole studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q2", "assignee": map[string]any{"type": "agent", "id": agent},
		"predicates": testPredicates}, &whole); code != 201 {
		t.Fatalf("create without scope: %d", code)
	}
	if whole.ScopeFrom != "grounding" || whole.ScopeTo != board.Stages[len(board.Stages)-1] {
		t.Fatalf("scope %s→%s", whole.ScopeFrom, whole.ScopeTo)
	}
}

func TestAOneAgentStudyStillNeedsPreregistration(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	var e struct{ Error string }
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": map[string]any{"type": "agent", "id": agent},
		"scope": map[string]string{"from": "experimentation", "to": "experimentation"}}, &e); code != 400 || e.Error != "a study that runs Experimentation needs at least one pre-registered result" {
		t.Fatalf("%d %q", code, e.Error)
	}
}

func TestAStudyIsWorkedByOneAgentOrByTeams(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	builtIn := s.builtInID(t, w.ID, "grounding")
	for name, tc := range map[string]struct {
		assignee map[string]any
		want     string
	}{
		"team with agent":  {map[string]any{"type": "team", "teams": map[string]string{"grounding": builtIn}, "agent_id": agent}, "a study is worked by one agent or by Teams, not both"},
		"agent with teams": {map[string]any{"type": "agent", "id": agent, "teams": map[string]string{"grounding": builtIn}}, "a study is worked by one agent or by Teams, not both"},
		"member":           {map[string]any{"type": "member", "id": s.userID(t, a)}, "a study is worked by one agent or by Teams"},
	} {
		var e struct{ Error string }
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": name, "assignee": tc.assignee,
			"scope": map[string]string{"from": "grounding", "to": "grounding"}}, &e); code != 400 || e.Error != tc.want {
			t.Errorf("%s: %d %q", name, code, e.Error)
		}
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 0 {
		t.Fatalf("%d studies", n)
	}
}

func TestAStudyRefusesAgentsTheResearcherCannotUse(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	admin := s.member(t, a, w.ID, "d@lab.org", "admin")
	grounding := map[string]string{"from": "grounding", "to": "grounding"}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "teamed", "scope": grounding}, nil); code != 201 {
		t.Fatalf("Team study: %d", code)
	}
	var copyID string
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT agent_id::text FROM team_agent LIMIT 1").Scan(&copyID); err != nil {
		t.Fatal(err)
	}
	archived := s.newAgent(t, a, w.ID, "gone", "claude")
	if code := s.call(t, "POST", "/api/agents/"+archived+"/archive", a, w.ID, nil, nil); code != 200 {
		t.Fatalf("archive: %d", code)
	}
	var private struct{ ID string }
	if code := s.call(t, "POST", "/api/agents", b, w.ID, map[string]string{"name": "mine", "harness": "claude", "visibility": "private"}, &private); code != 201 {
		t.Fatalf("private agent: %d", code)
	}
	aristotle := s.newAgent(t, a, w.ID, "ari", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE agent SET built_in_key = 'aristotle', visibility = 'private' WHERE id = $1", aristotle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tok, agent string
	}{
		{"archived", a, archived},
		{"colleague's private agent, as an admin", admin, private.ID},
		{"Aristotle", a, aristotle},
		{"a Team member's copy", a, copyID},
		{"unknown", a, uuid.NewString()},
	} {
		var e struct{ Error string }
		if code := s.call(t, "POST", "/api/cards", tc.tok, w.ID, map[string]any{"question": tc.name, "assignee": map[string]any{"type": "agent", "id": tc.agent},
			"scope": grounding}, &e); code != 400 || e.Error != "you can't start a study with this agent" {
			t.Errorf("%s: %d %q", tc.name, code, e.Error)
		}
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 1 {
		t.Fatalf("%d studies, want only the Team study", n)
	}
	shared := s.newAgent(t, b, w.ID, "shared", "claude")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "shared", "assignee": map[string]any{"type": "agent", "id": shared},
		"scope": grounding}, nil); code != 201 {
		t.Fatalf("a colleague's workspace agent: %d", code)
	}
}

func TestListedAgentsSayWhetherTheyAreTeamCopies(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	own := s.newAgent(t, a, w.ID, "solo", "claude")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "scope": map[string]string{"from": "grounding", "to": "grounding"}}, nil); code != 201 {
		t.Fatalf("Team study: %d", code)
	}
	var agents []struct {
		ID         string
		TeamMember *bool `json:"team_member"`
	}
	if code := s.call(t, "GET", "/api/agents", a, w.ID, nil, &agents); code != 200 {
		t.Fatalf("list: %d", code)
	}
	copies := 0
	for _, ag := range agents {
		isCopy := s.count(t, "SELECT count(*) FROM team_agent WHERE agent_id = $1", ag.ID) > 0
		if ag.TeamMember == nil || *ag.TeamMember != isCopy {
			t.Fatalf("agent %s: team_member %v, copy %v", ag.ID, ag.TeamMember, isCopy)
		}
		if isCopy {
			copies++
		}
		if ag.ID == own && *ag.TeamMember {
			t.Fatal("the researcher's own agent is marked a Team copy")
		}
	}
	if copies == 0 {
		t.Fatal("no Team copies listed")
	}
}

func TestNoStudyReachesPastValidation(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var e struct{ Error string }
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "scope": map[string]string{"from": "grounding", "to": "reporting"}}, &e); code != 400 || e.Error != "scope names a column outside the stages" {
		t.Fatalf("scope to reporting: %d %q", code, e.Error)
	}
	g := s.addedTeam(t, a, w.ID, "g", "grounding")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "reporting": g})}, nil); code != 400 {
		t.Fatalf("a Team on reporting: %d", code)
	}
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != 201 || c.ScopeTo != "validation" {
		t.Fatalf("default end: %d %+v", code, c)
	}
}

func TestCreateRefusesWhenAStageHasNoBuiltInTeam(t *testing.T) {
	s := newServer(t)
	s.builtIns(t, board.Stages[:3]...)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var e struct{ Error string }
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &e); code != 422 || e.Error != "No built-in Team for Validation" {
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
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != 201 {
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
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != 201 {
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

// uploadData posts one file with the given bytes and answers its upload id.
func (s *server) uploadData(t *testing.T, tok, wsID, name string, data []byte) string {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", name)
	part.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", s.URL+"/api/attachments", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Workspace-ID", wsID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ ID string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != 201 {
		t.Fatalf("upload %s: %d %v", name, resp.StatusCode, err)
	}
	return out.ID
}

func tinyPNG(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const attachedCSV = "gene,fold\nPGC1A,2.4\nTFAM,1.9\n"

// studyCalls is what a gate reads for a study's cites: its live call records
// under keys, with their whole answers.
func (s *server) studyCalls(t *testing.T, wsID, cardID string, keys ...string) map[string]flow.Call {
	t.Helper()
	var recs []db.Record
	err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(wsID), uuid.Nil, func(tx *store.Tx) error {
		var err error
		recs, err = tx.Q.LiveRecordsByKeys(t.Context(), db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(cardID), Keys: keys})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, err := flow.LoadCalls(t.Context(), s.H.Blobs, recs)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func TestAStudyLinksItsFilesAndRecordsThem(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	pic := tinyPNG(t)
	csv, png := s.uploadData(t, a, w.ID, "data.csv", []byte(attachedCSV)), s.uploadData(t, a, w.ID, "plot.png", pic)
	var c card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "Does fasting raise PGC1A?", "attachment_ids": []string{csv, png}}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	type linked struct{ disk, key string }
	got := map[string]linked{}
	rows, err := s.Env.Owner.Query(t.Context(), `SELECT id::text, disk_name, record_key FROM attachment WHERE card_id = $1`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var l linked
		if err := rows.Scan(&id, &l.disk, &l.key); err != nil {
			t.Fatal(err)
		}
		got[id] = l
	}
	if got[csv] != (linked{"data.csv", "c1"}) || got[png] != (linked{"plot.png", "c2"}) {
		t.Fatalf("links %+v", got)
	}
	type callBody struct {
		Server, Tool, Answer, At string
		Args                     struct {
			Name, Sha256 string
			SizeBytes    int64  `json:"size_bytes"`
			ContentType  string `json:"content_type"`
		}
		AnswerSHA256 string `json:"answer_sha256"`
		Truncated    bool
	}
	bodies := map[string]callBody{}
	blobs := map[string]string{}
	recs, err := s.Env.Owner.Query(t.Context(), `SELECT key, body, blob_key FROM record
		WHERE card_id = $1 AND kind = 'call' AND role = 'server' AND board_column = 'grounding' AND round = 0 AND agent_run_id IS NULL`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for recs.Next() {
		var key, blob string
		var raw []byte
		if err := recs.Scan(&key, &raw, &blob); err != nil {
			t.Fatal(err)
		}
		var b callBody
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatal(err)
		}
		bodies[key], blobs[key] = b, blob
	}
	picSum := sha256.Sum256(pic)
	csvSum := sha256.Sum256([]byte(attachedCSV))
	cb, pb := bodies["c1"], bodies["c2"]
	if len(bodies) != 2 || cb.Server != "attachments" || cb.Tool != "attach" || cb.Args.Name != "data.csv" || cb.Args.SizeBytes != int64(len(attachedCSV)) ||
		cb.Args.Sha256 != hex.EncodeToString(csvSum[:]) || cb.Args.ContentType != "text/csv" || cb.Answer != attachedCSV ||
		cb.AnswerSHA256 != hex.EncodeToString(csvSum[:]) || cb.Truncated || cb.At == "" {
		t.Fatalf("csv record %+v (%d records)", cb, len(bodies))
	}
	if want := "binary file plot.png, sha256 " + hex.EncodeToString(picSum[:]); pb.Answer != want || pb.Args.Sha256 != hex.EncodeToString(picSum[:]) || blobs["c2"] != "" {
		t.Fatalf("png record %+v blob %q", pb, blobs["c2"])
	}
	rc, err := s.H.Blobs.Get(t.Context(), blobs["c1"])
	if err != nil {
		t.Fatalf("csv blob %q: %v", blobs["c1"], err)
	}
	full, _ := io.ReadAll(rc)
	rc.Close()
	if string(full) != attachedCSV {
		t.Fatalf("csv blob %q", full)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'record.created'`, c.ID); n != 2 {
		t.Fatalf("record.created events: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'blob.created' AND payload->>'blob_key' = $2
		AND (payload->>'size')::bigint = $3`, c.ID, blobs["c1"], len(attachedCSV)); n != 1 {
		t.Fatalf("blob.created events: %d", n)
	}
}

func TestFilesKeepTheOrderTheyWereAttachedIn(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	// uploaded f0..f10, attached f10..f0: the order attached is the order kept
	var ids, names []string
	for i := range 11 {
		ids = append([]string{s.uploadData(t, a, w.ID, fmt.Sprintf("f%d.csv", i), []byte("x\n"))}, ids...)
		names = append([]string{fmt.Sprintf("f%d.csv", i)}, names...)
	}
	var c struct {
		ID          string
		Attachments []struct{ Name string }
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "attachment_ids": ids}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	listed := func() []string {
		var out []string
		for _, f := range c.Attachments {
			out = append(out, f.Name)
		}
		return out
	}
	if !slices.Equal(listed(), names) {
		t.Fatalf("created study lists %v, want %v", listed(), names)
	}
	for i, id := range ids {
		if n := s.count(t, `SELECT count(*) FROM attachment WHERE id = $1 AND record_key = $2`, id, fmt.Sprintf("c%d", i+1)); n != 1 {
			t.Fatalf("%s is not c%d", names[i], i+1)
		}
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID, a, w.ID, nil, &c); code != 200 || !slices.Equal(listed(), names) {
		t.Fatalf("study page lists %v (%d), want %v", listed(), code, names)
	}
}

func TestAQuoteFromAnAttachedFileIsChecked(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c, other card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "Does fasting raise PGC1A?",
		"attachment_ids": []string{s.uploadData(t, a, w.ID, "data.csv", []byte(attachedCSV))}}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "Does fasting raise TFAM?"}, &other); code != 201 {
		t.Fatalf("create other: %d", code)
	}
	claim := func(quote string) []db.Record {
		b, _ := json.Marshal(map[string]any{"claims": []any{map[string]any{"text": "PGC1A rises", "quote": quote, "cites": []string{"call:c1"}}}})
		return []db.Record{{Key: "r1", Kind: "claims", Body: b}}
	}
	own := s.studyCalls(t, w.ID, c.ID, "c1")
	if m := flow.MatchQuotes(claim("PGC1A,2.4"), own); len(m) != 1 || !m[0].Matched || m[0].Call != "c1" {
		t.Fatalf("a quote in the file: %+v", m)
	}
	if m := flow.MatchQuotes(claim("PGC1A,9.9"), own); len(m) != 1 || m[0].Matched {
		t.Fatalf("a quote not in the file: %+v", m)
	}
	// a file longer than a body keeps: the body has its start, quotes are checked against all of it
	big := strings.Repeat("gene,fold\n", 30000) + "SIRT1,3.1\n"
	var long card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "Does fasting raise SIRT1?",
		"attachment_ids": []string{s.uploadData(t, a, w.ID, "big.csv", []byte(big))}}, &long); code != 201 {
		t.Fatalf("create long: %d", code)
	}
	var body struct {
		Answer    string
		Truncated bool
	}
	var raw []byte
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT body FROM record WHERE card_id = $1 AND key = 'c1'`, long.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &body); !body.Truncated || len(body.Answer) != records.MaxCallAnswer || !strings.HasPrefix(big, body.Answer) {
		t.Fatalf("long body: truncated %v, %d bytes", body.Truncated, len(body.Answer))
	}
	if m := flow.MatchQuotes(claim("SIRT1,3.1"), s.studyCalls(t, w.ID, long.ID, "c1")); len(m) != 1 || !m[0].Matched {
		t.Fatalf("a quote past the body's cut: %+v", m)
	}
	// the other study has no c1: the attached file is not its source
	if m := flow.MatchQuotes(claim("PGC1A,2.4"), s.studyCalls(t, w.ID, other.ID, "c1")); len(m) != 1 || m[0].Matched {
		t.Fatalf("another study's file resolved: %+v", m)
	}
}

func TestStudyFileLimits(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var ids []string
	for i := range 6 {
		ids = append(ids, s.uploadData(t, a, w.ID, fmt.Sprintf("f%d.csv", i), []byte("x\n")))
	}
	many := slices.Clone(ids)
	for len(many) < 101 {
		many = append(many, uuid.NewString())
	}
	refused := func(ids []string, want string) {
		t.Helper()
		var out struct{ Error string }
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "attachment_ids": ids}, &out); code != 400 || out.Error != want {
			t.Fatalf("%d %q, want 400 %q", code, out.Error, want)
		}
		if n := s.count(t, `SELECT count(*) FROM card`); n != 0 {
			t.Fatalf("a refused study was created: %d", n)
		}
		if n := s.count(t, `SELECT count(*) FROM attachment WHERE card_id IS NULL AND disk_name = '' AND record_key = ''`); n != 6 {
			t.Fatalf("pending uploads: %d", n)
		}
		if n := s.count(t, `SELECT count(*) FROM record`); n != 0 {
			t.Fatalf("records: %d", n)
		}
	}
	refused(many, "A study can have at most 100 files")
	// Declared sizes stand in for five files totalling 10 GB and a byte.
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE attachment SET size_bytes = CASE WHEN id = $1 THEN 1 ELSE 2::bigint << 30 END`, ids[5]); err != nil {
		t.Fatal(err)
	}
	refused(ids, "A study's files can total at most 10 GB")
	// Exactly 10 GB is allowed.
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "attachment_ids": ids[:5]}, nil); code != 201 {
		t.Fatalf("10 GB: %d", code)
	}
}

func TestForeignUploadsAreRefusedAsUnknown(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	c := s.signIn(t, "c@else.org")
	wc := s.workspace(t, c, "else")
	mine := s.uploadData(t, a, w.ID, "mine.csv", []byte("x\n"))
	for name, id := range map[string]string{
		"a colleague's upload":       s.uploadData(t, b, w.ID, "theirs.csv", []byte("y\n")),
		"another workspace's upload": s.uploadData(t, c, wc.ID, "else.csv", []byte("z\n")),
		"an id that names no upload": uuid.NewString(),
	} {
		var out struct{ Error string }
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "attachment_ids": []string{mine, id}}, &out); code != 400 || out.Error != "Unknown attachment" {
			t.Fatalf("%s: %d %q", name, code, out.Error)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM card`); n != 0 {
		t.Fatalf("cards: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE card_id IS NULL`); n != 3 {
		t.Fatalf("pending uploads: %d", n)
	}
}

// gatedBlobs holds every Get until release is closed, saying when one waits.
type gatedBlobs struct {
	storage.Storage
	waiting chan struct{}
	release chan struct{}
}

func (g gatedBlobs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	g.waiting <- struct{}{}
	<-g.release
	return g.Storage.Get(ctx, key)
}

func TestAnUploadLinksToOneStudyOnly(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.uploadData(t, a, w.ID, "data.csv", []byte(attachedCSV))
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			codes[i] = s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": fmt.Sprintf("question %d", i), "attachment_ids": []string{id}}, nil)
		})
	}
	wg.Wait()
	slices.Sort(codes)
	if codes[0] != 201 || (codes[1] != 409 && codes[1] != 400) {
		t.Fatalf("codes %v", codes)
	}
	if n := s.count(t, `SELECT count(*) FROM card`); n != 1 {
		t.Fatalf("cards: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM record`); n != 1 {
		t.Fatalf("records: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment a JOIN card c ON c.id = a.card_id WHERE a.id = $1`, id); n != 1 {
		t.Fatalf("links: %d", n)
	}
}

func TestAnUploadTakenMidCreateRefusesTheStudy(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	first := s.uploadData(t, a, w.ID, "data.csv", []byte(attachedCSV))
	taken := s.uploadData(t, a, w.ID, "more.csv", []byte("gene\nSIRT1\n"))
	local := s.H.Blobs.(storage.Local)
	before := storedBlobs(t, local.Dir)
	if !slices.Contains(before, records.TextKey(w.ID+"/attachments/"+taken)) {
		t.Fatalf("fixture: the uploads' text is not stored: %v", before)
	}
	gate := gatedBlobs{local, make(chan struct{}, 2), make(chan struct{})}
	s.H.Blobs = gate
	code := make(chan int)
	go func() {
		code <- s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "attachment_ids": []string{first, taken}}, nil)
	}()
	// the study has read both uploads as pending; the sweep then takes one
	<-gate.waiting
	if _, err := s.Env.Owner.Exec(t.Context(), `DELETE FROM attachment WHERE id = $1`, taken); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	<-gate.waiting
	if c := <-code; c != 409 {
		t.Fatalf("create: %d", c)
	}
	if n := s.count(t, `SELECT count(*) FROM card`) + s.count(t, `SELECT count(*) FROM record`); n != 0 {
		t.Fatalf("the refused study left %d rows", n)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE id = $1 AND card_id IS NULL AND record_key = ''`, first); n != 1 {
		t.Fatal("the other upload is no longer pending")
	}
	// storage holds just what it held before the create
	if after := storedBlobs(t, local.Dir); !slices.Equal(after, before) {
		t.Fatalf("stored blobs %v, before the create %v", after, before)
	}
}

// storedBlobs lists every file under dir, sorted.
func storedBlobs(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func TestARunCannotRecordAnAttachment(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	for _, server := range []string{"attachments", " Attachments ", "ATTACHMENTS\t"} {
		in := map[string]any{"server": server, "tool": "attach", "args": map[string]any{"name": "forged.csv"}, "answer": "PGC1A,9.9"}
		if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", in, nil); c != 403 {
			t.Fatalf("a run recorded an attachment as %q: %d", server, c)
		}
	}
	if n := l.s.count(t, `SELECT count(*) FROM record WHERE kind = 'call' AND lower(trim(body->>'server')) = 'attachments'`); n != 0 {
		t.Fatalf("forged attachment records: %d", n)
	}
}

func TestAnUploadRecordsItsTextHash(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	csv, png := s.uploadData(t, a, w.ID, "data.csv", []byte(attachedCSV)), s.uploadData(t, a, w.ID, "plot.png", tinyPNG(t))
	sum := sha256.Sum256([]byte(attachedCSV))
	text := func(id string) (sha, blob string) {
		t.Helper()
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT text_sha256, blob_key FROM attachment WHERE id = $1`, id).Scan(&sha, &blob); err != nil {
			t.Fatal(err)
		}
		return sha, blob
	}
	sha, blob := text(csv)
	rc, err := s.H.Blobs.Get(t.Context(), records.TextKey(blob))
	if sha != hex.EncodeToString(sum[:]) || err != nil {
		t.Fatalf("csv text %q: %v", sha, err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != attachedCSV {
		t.Fatalf("csv text blob %q", got)
	}
	sha, blob = text(png)
	if _, err := s.H.Blobs.Get(t.Context(), records.TextKey(blob)); sha != "" || !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("png text %q: %v", sha, err)
	}
	// a PDF's text layer is read from the stored upload
	doc, err := os.ReadFile("../records/testdata/finding.pdf")
	if err != nil {
		t.Fatal(err)
	}
	sha, blob = text(s.uploadData(t, a, w.ID, "finding.pdf", doc))
	rc, err = s.H.Blobs.Get(t.Context(), records.TextKey(blob))
	if err != nil {
		t.Fatalf("pdf text: %v", err)
	}
	got, _ = io.ReadAll(rc)
	rc.Close()
	pdfSum := sha256.Sum256(got)
	if !strings.Contains(string(got), "Mitochondrial density rose in fasting mice.") || sha != hex.EncodeToString(pdfSum[:]) {
		t.Fatalf("pdf text %q, sha256 %q", got, sha)
	}
}

func TestASlowExtractionIsRecordedAsBinary(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	stuck := make(chan struct{})
	defer close(stuck)
	s.H.TextDeadline = 100 * time.Millisecond
	s.H.ExtractText = func(name, typ string, f io.ReaderAt, size int64) (*io.SectionReader, bool, error) {
		<-stuck
		return io.NewSectionReader(strings.NewReader("never"), 0, 5), true, nil
	}
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	pdf := []byte("%PDF-1.4\nspins forever\n")
	start := time.Now()
	id := s.uploadData(t, a, w.ID, "slow.pdf", pdf)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the upload waited %v for the parse", d)
	}
	var c card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q", "attachment_ids": []string{id}}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	sum := sha256.Sum256(pdf)
	var answer, blob string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT body->>'answer', blob_key FROM record WHERE card_id = $1 AND key = 'c1'`, c.ID).Scan(&answer, &blob); err != nil {
		t.Fatal(err)
	}
	if answer != "binary file slow.pdf, sha256 "+hex.EncodeToString(sum[:]) || blob != "" {
		t.Fatalf("record %q blob %q", answer, blob)
	}
}

func TestAtMostThreeUploadsReadTheirTextAtOnce(t *testing.T) {
	s := newServer(t)
	var running, most atomic.Int32
	s.H.ExtractText = func(name, typ string, f io.ReaderAt, size int64) (*io.SectionReader, bool, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(150 * time.Millisecond)
		return records.ExtractText(name, typ, f, size)
	}
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { s.uploadData(t, a, w.ID, fmt.Sprintf("f%d.csv", i), []byte("x\n")) })
	}
	wg.Wait()
	if m := most.Load(); m != maxExtractions {
		t.Fatalf("%d text reads ran at once, want %d", m, maxExtractions)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE text_sha256 <> ''`); n != 8 {
		t.Fatalf("%d of 8 uploads kept their text", n)
	}
}

func TestAnAbandonedParseEndsWhenItsFileIsClosed(t *testing.T) {
	s := newServer(t)
	s.H.TextDeadline = 100 * time.Millisecond
	ended := make(chan error, 1)
	// a parse that never finishes on its own: it reads until reading fails
	s.H.ExtractText = func(name, typ string, f io.ReaderAt, size int64) (*io.SectionReader, bool, error) {
		b := make([]byte, 1)
		for {
			if _, err := f.ReadAt(b, 0); err != nil {
				ended <- err
				return nil, false, err
			}
			time.Sleep(time.Millisecond)
		}
	}
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.uploadData(t, a, w.ID, "slow.pdf", []byte("%PDF-1.4\n"))
	select {
	case err := <-ended:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("the parse ended on %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the abandoned parse still reads its file")
	}
	// its slot is free again
	deadline := time.Now().Add(5 * time.Second)
	for len(extractions) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(extractions); n != 0 {
		t.Fatalf("%d slots held", n)
	}
}

func TestStudyVariablesAreRefused(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "variables@lab.org")
	w := s.workspace(t, a, "variables")
	var out struct {
		Error string `json:"error"`
	}
	code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "variables", "env": map[string]string{}}, &out)
	if code != 400 || !strings.Contains(out.Error, `unknown field "env"`) {
		t.Fatalf("status %d: %+v", code, out)
	}
}
