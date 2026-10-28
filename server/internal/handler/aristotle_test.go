package handler

import (
	"encoding/json"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAristotleBootstrap(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	var first, again struct {
		Agent        struct{ ID string }
		Conversation struct{ ID string }
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &first); code != 200 {
		t.Fatalf("bootstrap: %d", code)
	}
	if first.Agent.ID == "" || first.Conversation.ID == "" {
		t.Fatalf("missing identity: %+v", first)
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &again); code != 200 || again.Agent.ID != first.Agent.ID || again.Conversation.ID != first.Conversation.ID {
		t.Fatalf("repeat: %d %+v", code, again)
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", b, ws, nil, &again); code != 200 || again.Agent.ID == first.Agent.ID || again.Conversation.ID == first.Conversation.ID {
		t.Fatalf("personal: %d %+v", code, again)
	}
	if code := s.call(t, "POST", "/api/agents/"+first.Agent.ID+"/archive", a, ws, nil, nil); code != 409 {
		t.Fatalf("archive: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+first.Agent.ID, a, ws, map[string]string{"visibility": "workspace"}, nil); code != 400 {
		t.Fatalf("visibility: %d", code)
	}
}

func TestAristotleSettingsAndPrivacy(t *testing.T) {
	s, a, b, c, ws, _ := csFixture(t)
	// Ordinary Aristotle remains ordinary and does not consume the builtin identity.
	ordinary := map[string]any{"name": "Aristotle", "harness": "claude"}
	if code := s.call(t, "POST", "/api/agents", a, ws, ordinary, nil); code != 201 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", "/api/agents", b, ws, ordinary, nil); code != 409 {
		t.Fatalf("ordinary uniqueness: %d", code)
	}
	var boot struct {
		Agent        db.Agent                  `json:"agent"`
		Conversation db.CoresearchConversation `json:"conversation"`
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &boot); code != 200 {
		t.Fatal(code)
	}
	if boot.Agent.BuiltInKey != "aristotle" || boot.Agent.Visibility != "private" || boot.Agent.Harness != "claude" || boot.Agent.Model != "" {
		t.Fatalf("defaults: %+v", boot.Agent)
	}
	path := "/api/agents/" + boot.Agent.ID.String()
	starters := []ConversationStarter{{Label: "Second", Prompt: "Do this"}, {Label: "First", Prompt: "Do that"}}
	var edited db.Agent
	if code := s.call(t, "PATCH", path, a, ws, map[string]any{"name": "Mine", "instructions": "Custom", "model": "custom-model", "conversation_starters": starters}, &edited); code != 200 {
		t.Fatal(code)
	}
	var again struct {
		Agent        db.Agent
		Conversation db.CoresearchConversation
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &again); code != 200 || again.Agent.Name != "Mine" || again.Agent.Instructions != "Custom" || string(again.Agent.ConversationStarters) != string(edited.ConversationStarters) {
		t.Fatalf("customization overwritten %d %+v", code, again)
	}
	if code := s.call(t, "GET", path, b, ws, nil, nil); code != 404 {
		t.Fatalf("private agent: %d", code)
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", c, ws, nil, nil); code != 404 {
		t.Fatalf("outsider: %d", code)
	}
	var projects []csProject
	if code := s.call(t, "GET", csProjects, a, ws, nil, &projects); code != 200 || len(projects) != 1 {
		t.Fatalf("project list: %d %+v", code, projects)
	}
	backing := csProjects + "/" + boot.Conversation.ProjectID.String()
	for _, tok := range []string{b, c} {
		if code := s.call(t, "GET", backing, tok, ws, nil, nil); code != 404 {
			t.Fatalf("private project: %d", code)
		}
	}
	for _, req := range []struct {
		method, path string
		body         any
	}{{"POST", backing + "/members", map[string]string{"user_id": s.userID(t, b), "role": "owner"}}, {"POST", backing + "/archive", nil}, {"PATCH", backing, map[string]string{"name": "Changed"}}} {
		if code := s.call(t, req.method, req.path, a, ws, req.body, nil); code != 403 {
			t.Fatalf("private backing mutation %s: %d", req.path, code)
		}
	}
	var list []db.ListCoresearchConversationsRow
	if code := s.call(t, "GET", "/api/coresearch/conversations", a, ws, nil, &list); code != 200 || len(list) != 1 || list[0].ID != boot.Conversation.ID {
		t.Fatalf("conversation list: %d %+v", code, list)
	}
	if code := s.call(t, "GET", "/api/coresearch/conversations", b, ws, nil, &list); code != 200 || len(list) != 0 {
		t.Fatalf("personal list: %d %+v", code, list)
	}
	otherWS := s.workspace(t, a, "other").ID
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, otherWS, nil, &again); code != 200 || again.Agent.ID == boot.Agent.ID || again.Conversation.ID == boot.Conversation.ID {
		t.Fatalf("workspace isolation: %d %+v", code, again)
	}
	for _, body := range []any{map[string]any{"conversation_starters": []ConversationStarter{{Label: "", Prompt: "x"}}}, map[string]any{"built_in_key": "aristotle"}} {
		if code := s.call(t, "PATCH", path, a, ws, body, nil); code != 400 {
			t.Fatalf("invalid settings: %d", code)
		}
	}
	if code := s.call(t, "POST", "/api/agents", a, ws, map[string]any{"name": "forged", "harness": "claude", "built_in_key": "aristotle"}, nil); code != 400 {
		t.Fatalf("forged builtin: %d", code)
	}
	if code := s.call(t, "PATCH", path, a, ws, map[string]any{"conversation_starters": []ConversationStarter{}}, &edited); code != 200 || string(edited.ConversationStarters) != "[]" {
		t.Fatalf("defaults: %d %s", code, edited.ConversationStarters)
	}
}

func TestAristotleConcurrentBootstrap(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct{ Conversation struct{ ID string } }
			if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &out); code != 200 {
				t.Errorf("bootstrap: %d", code)
			}
			ids <- out.Conversation.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id == "" || id != first {
			t.Fatalf("duplicate welcome %q %q", id, first)
		}
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_project WHERE workspace_id=$1 AND conversation_owner_id IS NOT NULL`, ws).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backing projects %d %v", n, err)
	}
}

func TestConversationStarterValidation(t *testing.T) {
	for _, raw := range []string{`null`, ` null `, `{}`, `[{"label":"","prompt":"x"}]`, `[{"label":"x","prompt":""}]`, `[{"label":"x\u0000","prompt":"x"}]`} {
		if validateConversationStarters(json.RawMessage(raw)) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, starters := range [][]ConversationStarter{make([]ConversationStarter, 13), {{Label: strings.Repeat("x", 121), Prompt: "x"}}, {{Label: "x", Prompt: strings.Repeat("x", 16385)}}} {
		raw, _ := json.Marshal(starters)
		if validateConversationStarters(raw) == nil {
			t.Fatalf("accepted oversized starters")
		}
	}
}

func TestAristotleMemberRemoval(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	var boot struct {
		Agent        db.Agent
		Conversation db.CoresearchConversation
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", b, ws, nil, &boot); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", "/api/agents", b, ws, map[string]string{"name": "Aristotle", "harness": "claude"}, nil); code != 201 {
		t.Fatalf("ordinary after builtin: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/workspaces/"+ws+"/members/"+s.userID(t, b), a, "", nil, nil); code != 204 {
		t.Fatalf("member removal with builtin: %d", code)
	}
	var archived bool
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT archived_at IS NOT NULL FROM agent WHERE id=$1`, boot.Agent.ID).Scan(&archived); err != nil || archived {
		t.Fatalf("builtin archived: %t %v", archived, err)
	}
}

func TestConversationStarterBoundaries(t *testing.T) {
	starters := []ConversationStarter{{Label: strings.Repeat("界", 120), Prompt: strings.Repeat("x", 16384)}}
	raw, _ := json.Marshal(starters)
	if err := validateConversationStarters(raw); err != nil {
		t.Fatalf("valid boundary: %v", err)
	}
	if err := validateConversationStarters(json.RawMessage("[{\"label\":\"\xff\",\"prompt\":\"x\"}]")); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

// Pause departure while archiving an enumerated ordinary project, before
// membership deletion. Bootstrap can then create a new project outside that
// enumeration unless the workspace mutation lock serializes both paths.
func TestAristotleBootstrapSerializesWithDeparture(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	ctx := t.Context()
	if code := s.call(t, "POST", csProjects, b, ws, map[string]string{"name": "Departure barrier"}, nil); code != 201 {
		t.Fatal(code)
	}
	if _, err := s.Env.Owner.Exec(ctx, `CREATE FUNCTION test_departure_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(9827345); RETURN NEW; END $$; CREATE TRIGGER test_departure_barrier AFTER UPDATE OF archived_at ON coresearch_project FOR EACH ROW WHEN (NEW.archived_at IS NOT NULL) EXECUTE FUNCTION test_departure_barrier()`); err != nil {
		t.Fatal(err)
	}
	barrier, err := s.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback(ctx)
	if _, err = barrier.Exec(ctx, `SELECT pg_advisory_xact_lock(9827345)`); err != nil {
		t.Fatal(err)
	}
	removed := make(chan int, 1)
	go func() {
		removed <- s.call(t, "DELETE", "/api/workspaces/"+ws+"/members/"+s.userID(t, b), a, "", nil, nil)
	}()
	waitFor := func(fragment string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var n int
			if err := s.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE $1`, "%"+fragment+"%").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("did not reach advisory barrier: %s", fragment)
	}
	waitFor("CoresearchDepart")
	bootstrapped := make(chan int, 1)
	go func() { bootstrapped <- s.call(t, "POST", "/api/coresearch/bootstrap", b, ws, nil, nil) }()
	// Observe either bootstrap's workspace lock wait or completion, without releasing the
	// deletion barrier. Completion before departure commits reproduces the bug.
	deadline := time.Now().Add(10 * time.Second)
	early := 0
	blocked := false
	for time.Now().Before(deadline) {
		select {
		case early = <-bootstrapped:
		default:
		}
		if early != 0 {
			break
		}
		var n int
		if err := s.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockWorkspaceForMutation%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if code := <-removed; code != 204 {
		t.Fatalf("departure: %d", code)
	}
	code := early
	if early == 0 {
		code = <-bootstrapped
	}
	var orphaned int
	if err := s.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM coresearch_project p WHERE p.workspace_id=$1 AND p.conversation_owner_id=$2 AND p.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=p.workspace_id AND m.user_id=p.conversation_owner_id) AND NOT EXISTS(SELECT 1 FROM coresearch_conversation c WHERE c.project_id=p.id)`, ws, s.userID(t, b)).Scan(&orphaned); err != nil {
		t.Fatal(err)
	}
	if !blocked || code != 404 || orphaned != 0 {
		t.Fatalf("bootstrap must wait for departure then recheck membership: blocked=%t status=%d orphaned=%d", blocked, code, orphaned)
	}
}
