package handler

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestStudyProposalSaveAndLaunch(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "researcher@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.stagedAgent(t, a, w.ID, "grounding")
	path := "/api/study-proposals/" + uuid.NewString()
	input := map[string]any{"revision": 0, "description": "  Quantify α and β  ", "stage": "grounding", "assignee": map[string]any{"type": "agent", "agent_id": agent}}
	// A stale suggestion can outlive the private chat on an offline phone.
	input["from_suggestion"] = true
	if code := s.call(t, "PUT", path, a, w.ID, input, nil); code != 409 {
		t.Fatalf("saved missing suggestion: %d", code)
	}
	input["operation_id"] = uuid.NewString()
	if code := s.call(t, "POST", path+"/start", a, w.ID, input, nil); code != 409 {
		t.Fatalf("started missing suggestion: %d", code)
	}
	delete(input, "from_suggestion")
	delete(input, "operation_id")
	for i := 0; i < 2; i++ {
		var p map[string]any
		if code := s.call(t, "PUT", path, a, w.ID, input, &p); code != 200 || p["revision"] != float64(1) {
			t.Fatalf("save %d %v", code, p)
		}
	}
	s.H.Engine.Inline = true
	if err := s.H.Engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT count(*) FROM card WHERE workspace_id=$1", "SELECT count(*) FROM agent_run WHERE workspace_id=$1", "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='card.created'"} {
		var n int
		if err := s.Env.Owner.QueryRow(t.Context(), q, w.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("save started work: %d %v", n, err)
		}
	}
	op := map[string]any{"revision": 1, "operation_id": uuid.NewString()}
	var receipt map[string]any
	if c := s.call(t, "POST", path+"/start", a, w.ID, op, &receipt); c != 200 {
		t.Fatalf("start %d %v", c, receipt)
	}
	var discarded map[string]any
	if c := s.call(t, "DELETE", path+"?revision=1", a, w.ID, nil, &discarded); c != 409 || discarded["card_id"] != receipt["card_id"] || discarded["number"] != receipt["number"] {
		t.Fatalf("discard after promotion: %d %v", c, discarded)
	}
	var retry map[string]any
	if c := s.call(t, "POST", path+"/start", a, w.ID, op, &retry); c != 200 || retry["card_id"] != receipt["card_id"] {
		t.Fatalf("retry %d %v", c, retry)
	}
}

func TestStudyProposalOwnershipDiscardAndProjectRemoval(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab").ID
	otherWS := s.workspace(t, owner, "other").ID
	colleague := s.member(t, owner, ws, "member@lab.org", "member")
	agent := s.stagedAgent(t, colleague, ws, "validation")
	var project struct{ ID string }
	if c := s.call(t, "POST", "/api/researches", owner, ws, map[string]string{"name": "Field study"}, &project); c != 201 {
		t.Fatal(c)
	}
	base := "/api/study-proposals"
	path := base + "/" + uuid.NewString()
	input := map[string]any{"revision": 0, "description": "Test variation", "project_id": project.ID, "stage": "validation", "assignee": map[string]any{"type": "agent", "agent_id": agent}}
	if c := s.call(t, "PUT", path, colleague, ws, input, nil); c != 200 {
		t.Fatal(c)
	}
	for _, test := range []struct {
		ws, filter string
		n          int
	}{
		{ws, "", 1}, {ws, "?owner_id=" + s.userID(t, colleague), 1}, {ws, "?owner_id=" + s.userID(t, owner), 0},
		{ws, "?project_id=" + project.ID, 1}, {ws, "?project_id=" + uuid.NewString(), 0}, {otherWS, "", 0}} {
		var out struct{ Items []map[string]any }
		if c := s.call(t, "GET", base+test.filter, owner, test.ws, nil, &out); c != 200 || len(out.Items) != test.n {
			t.Fatalf("list %s %d %v", test.filter, c, out)
		}
	}
	if c := s.call(t, "PUT", path, owner, ws, input, nil); c != 403 {
		t.Fatal("non-owner edit", c)
	}
	if c := s.call(t, "DELETE", path+"?revision=1", owner, ws, nil, nil); c != 403 {
		t.Fatal("non-owner discard", c)
	}
	if c := s.call(t, "DELETE", "/api/researches/"+project.ID, owner, ws, nil, nil); c != 204 {
		t.Fatal(c)
	}
	input["revision"] = 1
	if c := s.call(t, "PUT", path, colleague, ws, input, nil); c != 409 {
		t.Fatal("stale edit", c)
	}
	if c := s.call(t, "POST", path+"/start", colleague, ws, map[string]any{"revision": 1, "operation_id": uuid.NewString()}, nil); c != 409 {
		t.Fatal("stale start", c)
	}
	for range 2 {
		if c := s.call(t, "DELETE", path+"?revision=2", colleague, ws, nil, nil); c != 204 {
			t.Fatal("discard/replay", c)
		}
	}
	delete(input, "project_id")
	input["revision"] = 0
	if c := s.call(t, "PUT", path, colleague, ws, input, nil); c != 409 {
		t.Fatal("discarded proposal revived", c)
	}
	if c := s.call(t, "PUT", base+"/"+uuid.NewString(), colleague, ws, input, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "DELETE", "/api/workspaces/"+ws+"/members/"+s.userID(t, colleague), owner, ws, nil, nil); c != 204 {
		t.Fatal("remove member", c)
	}
	var out struct{ Items []map[string]any }
	if c := s.call(t, "GET", base, owner, ws, nil, &out); c != 200 || len(out.Items) != 0 {
		t.Fatal("departed proposals", c, out)
	}
	var retired int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM study_proposal WHERE workspace_id=$1 AND discarded_at IS NOT NULL AND description IS NULL AND stage IS NULL AND project_id IS NULL AND assignee IS NULL`, ws).Scan(&retired); err != nil || retired != 2 {
		t.Fatal("tombstones retain proposal content", retired, err)
	}

}

// Two devices can choose different operation IDs for the same suggestion.
// Simulate losing the post-commit wakeup by leaving it to the next engine tick.
func TestStudyProposalConcurrentDirectStartAndRecovery(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "validation@lab.org")
	ws := s.workspace(t, tok, "lab").ID
	pack := s.addedTeam(t, tok, ws, "v", "validation")
	engine := s.H.Engine
	s.H.Engine = nil
	path := "/api/study-proposals/" + uuid.NewString() + "/start"
	question := strings.Repeat("Δ", 82)
	makeInput := func() map[string]any {
		return map[string]any{"description": question, "revision": 0, "operation_id": uuid.NewString(), "assignee": map[string]any{"type": "team", "pack_id": pack}, "stage": "validation"}
	}
	type result struct {
		code    int
		receipt proposalReceipt
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			var r result
			defer func() { results <- r }()
			r.code = s.call(t, "POST", path, tok, ws, makeInput(), &r.receipt)
		}()
	}
	first, second := <-results, <-results
	if first.code != 200 || second.code != 200 || first.receipt != second.receipt || first.receipt.CardID == uuid.Nil {
		t.Fatalf("concurrent starts: %+v %+v", first, second)
	}
	id := first.receipt.CardID
	var title, description, stage, team string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT c.title,c.question,c.board_column,t.pack_id::text FROM card c JOIN card_team t
 ON c.id=t.card_id AND c.workspace_id=t.workspace_id AND t.board_column=c.board_column WHERE c.id=$1`, id).Scan(&title, &description, &stage, &team); err != nil || title != strings.Repeat("Δ", 79)+"…" || description != question || stage != "validation" || team != pack {
		t.Fatalf("wrong card %s/%s/%s/%s %v", title, description, stage, team, err)
	}
	var dup map[string]any
	if c := s.call(t, "POST", "/api/study-proposals/"+uuid.NewString()+"/start", tok, ws, makeInput(), &dup); c != 409 || dup["card_id"] != id.String() {
		t.Fatal("independent duplicate", c, dup)
	}
	different := makeInput()
	different["operation_id"] = first.receipt.OperationID
	different["description"] = "Another question"
	if c := s.call(t, "POST", path, tok, ws, different, nil); c != 409 {
		t.Fatal("same operation changed input", c)
	}
	if c := s.call(t, "POST", "/api/study-proposals/"+uuid.NewString()+"/start", tok, ws, different, nil); c != 409 {
		t.Fatal("same operation across proposal IDs", c)
	}
	engine.Inline = true
	for range 2 {
		if err := engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var runs, created int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM agent_run WHERE card_id=$1 AND status IN ('queued','dispatched')),
 (SELECT count(*) FROM event WHERE card_id=$1 AND type='card.created')`, id).Scan(&runs, &created); err != nil || runs != 1 || created != 1 {
		t.Fatal("recovery", runs, created, err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "DELETE FROM card WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	var retry proposalReceipt
	if c := s.call(t, "POST", path, tok, ws, makeInput(), &retry); c != 200 || retry != first.receipt {
		t.Fatal("replay after deletion", c, retry)
	}
}

func TestStudyProposalInvalidatedAssignment(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "agent@lab.org")
	ws := s.workspace(t, tok, "lab").ID
	agent := s.stagedAgent(t, tok, ws, "grounding")
	path := "/api/study-proposals/" + uuid.NewString()
	input := map[string]any{"revision": 0, "description": "Compare instruments", "stage": "validation", "assignee": map[string]any{"type": "agent", "agent_id": agent}}
	if c := s.call(t, "PUT", path, tok, ws, input, nil); c != 400 {
		t.Fatal("wrong stage", c)
	}
	input["stage"] = "grounding"
	if c := s.call(t, "PUT", path, tok, ws, input, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", "/api/agents/"+agent+"/archive", tok, ws, nil, nil); c != 204 && c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", path+"/start", tok, ws, map[string]any{"revision": 1, "operation_id": uuid.NewString()}, nil); c != 400 {
		t.Fatal("archived agent started", c)
	}
	var out struct{ Items []store.StudyProposal }
	if c := s.call(t, "GET", "/api/study-proposals", tok, ws, nil, &out); c != 200 || len(out.Items) != 1 || out.Items[0].Revision != 1 {
		t.Fatal("failed launch changed draft", c, out)
	}
}

func TestStudyProposalMigrationRoundTrip(t *testing.T) {
	env := testutil.Setup(t)
	for _, suffix := range []string{"down", "up"} {
		sql, err := migrations.FS.ReadFile("0084_study_proposals." + suffix + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.Owner.Exec(t.Context(), string(sql)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStudyProposalDiscardRacesStart(t *testing.T) {
	s := newServer(t)
	user := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, user, "lab").ID
	agent := s.stagedAgent(t, user, ws, "validation")
	path := "/api/study-proposals/" + uuid.NewString()
	input := map[string]any{"description": "Compare survival across cohorts", "stage": "validation", "assignee": map[string]any{"type": "agent", "agent_id": agent}, "revision": 0}
	if c := s.call(t, "PUT", path, user, ws, input, nil); c != 200 {
		t.Fatal(c)
	}
	start := map[string]any{"revision": 1, "operation_id": uuid.NewString()}
	done := make(chan int, 1)
	go func() {
		var code int
		defer func() { done <- code }()
		code = s.call(t, "POST", path+"/start", user, ws, start, nil)
	}()
	discard := s.call(t, "DELETE", path+"?revision=1", user, ws, nil, nil)
	launch := <-done
	n := s.count(t, "SELECT count(*) FROM card WHERE workspace_id=$1", ws)
	if !(launch == 200 && discard == 409 && n == 1 || launch == 409 && discard == 204 && n == 0) {
		t.Fatalf("start=%d, discard=%d, cards=%d", launch, discard, n)
	}
	if retry := s.call(t, "POST", path+"/start", user, ws, start, nil); retry != launch {
		t.Fatalf("start retry=%d, first=%d", retry, launch)
	}
}
