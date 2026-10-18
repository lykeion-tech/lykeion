package handler

import (
	"testing"
	"time"
)

func TestCardListShowsQueuedAndTheWorkingRole(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var idle, waiting, working, stopped card
	for i, c := range []*card{&idle, &waiting, &working, &stopped} {
		if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q" + itoa(int64(i))}, c); code != 201 {
			t.Fatal(code)
		}
	}
	owner := s.userID(t, a)
	s.queue(t, w.ID, waiting.ID, owner, "claude")
	s.queue(t, w.ID, working.ID, owner, "claude")
	s.queue(t, w.ID, stopped.ID, owner, "claude")
	ctx := t.Context()
	// waiting: a queued run held back for budget. working: an older run done, a
	// newer reviewer running. stopped: its only run was cancelled.
	for _, q := range []string{
		`UPDATE agent_run SET queue_reason = 'budget' WHERE card_id = '` + waiting.ID + `'`,
		`UPDATE agent_run SET status = 'completed', created_at = now() - interval '1 hour' WHERE card_id = '` + working.ID + `'`,
		`UPDATE agent_run SET status = 'cancelled' WHERE card_id = '` + stopped.ID + `'`,
		`UPDATE card SET board_column = 'grounding', position = 0 WHERE id IN ('` + waiting.ID + `', '` + working.ID + `')`,
	} {
		if _, err := s.Env.Owner.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s.queue(t, w.ID, working.ID, owner, "codex")
	if _, err := s.Env.Owner.Exec(ctx, `UPDATE agent_run SET status = 'running', role = 'grounding-reviewer' WHERE card_id = $1 AND harness = 'codex'`, working.ID); err != nil {
		t.Fatal(err)
	}

	var list []struct {
		ID          string `json:"id"`
		BoardColumn string `json:"board_column"`
		Queued      bool   `json:"queued"`
		Role        string `json:"role"`
	}
	if code := s.call(t, "GET", "/api/cards", a, w.ID, nil, &list); code != 200 || len(list) != 4 {
		t.Fatalf("%d %+v", code, list)
	}
	got := map[string]string{}
	for _, c := range list {
		got[c.ID] = c.BoardColumn + "/" + map[bool]string{true: "queued", false: "-"}[c.Queued] + "/" + c.Role
	}
	want := map[string]string{
		idle.ID:    "backlog/-/",
		waiting.ID: "grounding/queued/scoper",
		working.ID: "grounding/-/grounding-reviewer",
		stopped.ID: "backlog/-/",
	}
	for id, exp := range want {
		if got[id] != exp {
			t.Errorf("card %s: %q, want %q", id, got[id], exp)
		}
	}
	// Grouped by column, then Backlog's position order.
	if list[0].ID != stopped.ID || list[1].ID != idle.ID || list[2].BoardColumn != "grounding" {
		t.Errorf("order: %+v", list)
	}
}

func TestCardRunsListOldestFirst(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	w, other := s.workspace(t, a, "lab"), s.workspace(t, b, "else")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	owner := s.userID(t, a)
	s.queue(t, w.ID, c.ID, owner, "claude")
	s.queue(t, w.ID, c.ID, owner, "codex")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET created_at = now() - interval '1 minute' WHERE card_id = $1 AND harness = 'codex'`, c.ID); err != nil {
		t.Fatal(err)
	}
	var runs []struct {
		ID          string  `json:"id"`
		Role        string  `json:"role"`
		BoardColumn string  `json:"board_column"`
		Round       int     `json:"round"`
		Status      string  `json:"status"`
		Harness     string  `json:"harness"`
		RuntimeID   *string `json:"runtime_id"`
		StartedAt   *string `json:"started_at"`
		CompletedAt *string `json:"completed_at"`
		CostUsd     float64 `json:"cost_usd"`
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/runs", a, w.ID, nil, &runs); code != 200 {
		t.Fatalf("runs: %d", code)
	}
	if len(runs) != 2 || runs[0].Harness != "codex" || runs[1].Harness != "claude" || runs[0].Role != "scoper" ||
		runs[0].BoardColumn != "grounding" || runs[0].Round != 1 || runs[0].Status != "queued" || runs[0].RuntimeID != nil {
		t.Fatalf("%+v", runs)
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/runs", b, other.ID, nil, nil); code != 404 {
		t.Fatalf("runs across workspaces: %d", code)
	}
}

func TestCardListUpdatedAtIsTheNewestEvent(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var busy, quiet card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "busy"}, &busy)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "quiet"}, &quiet)
	for _, q := range []string{
		`UPDATE event SET created_at = '2030-01-02T03:04:05Z' WHERE card_id = '` + busy.ID + `'`,
		`UPDATE card SET created_at = '2020-01-01T00:00:00Z' WHERE id IN ('` + busy.ID + `', '` + quiet.ID + `')`,
		`DELETE FROM event WHERE card_id = '` + quiet.ID + `'`,
	} {
		if _, err := s.Env.Owner.Exec(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	var list []struct {
		ID        string    `json:"id"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if code := s.call(t, "GET", "/api/cards", a, w.ID, nil, &list); code != 200 || len(list) != 2 {
		t.Fatalf("%d %+v", code, list)
	}
	want := map[string]string{busy.ID: "2030-01-02T03:04:05Z", quiet.ID: "2020-01-01T00:00:00Z"}
	for _, c := range list {
		if got := c.UpdatedAt.UTC().Format(time.RFC3339); got != want[c.ID] {
			t.Errorf("card %s updated_at %s, want %s", c.ID, got, want[c.ID])
		}
	}
}

func TestCardRecordsCanIncludeSuperseded(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, superseded_at, created_at)
		VALUES ($1, $2, 'grounding', 1, 'scope', 'scoper', 'old', now(), now() - interval '1 second'), ($1, $2, 'grounding', 2, 'scope', 'scoper', 'new', NULL, now())`, w.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	type rec struct {
		Key          string  `json:"key"`
		SupersededAt *string `json:"superseded_at"`
	}
	var live, all []rec
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/records?column=grounding", a, w.ID, nil, &live); code != 200 || len(live) != 1 || live[0].Key != "new" {
		t.Fatalf("default: %d %+v", code, live)
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/records?column=grounding&all=1", a, w.ID, nil, &all); code != 200 || len(all) != 2 {
		t.Fatalf("all: %d %+v", code, all)
	}
	if all[0].Key != "old" || all[0].SupersededAt == nil || all[1].Key != "new" || all[1].SupersededAt != nil {
		t.Fatalf("all: %+v", all)
	}
}

func TestRecordsWrittenTogetherListInKeyOrder(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, created_at)
		SELECT $1, $2, 'grounding', 1, 'score', 'server', k, '2030-01-01T00:00:00Z' FROM unnest(ARRAY['r10','r9','r2','r1']) k`, w.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	var all []struct {
		Key string `json:"key"`
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/records?column=grounding&all=1", a, w.ID, nil, &all); code != 200 || len(all) != 4 {
		t.Fatalf("%d %+v", code, all)
	}
	for i, k := range []string{"r1", "r2", "r9", "r10"} {
		if all[i].Key != k {
			t.Fatalf("order %+v", all)
		}
	}
}
