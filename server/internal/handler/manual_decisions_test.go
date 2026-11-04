package handler

import (
	"context"
	"strings"
	"testing"
	"time"
)

type manualRow struct {
	decisionRow
	Title      string
	Context    string
	Deadline   *string
	Status     string
	UserID     string `json:"user_id"`
	CanEdit    bool   `json:"can_edit"`
	CanResolve bool   `json:"can_resolve"`
}

func (s *server) createManual(t *testing.T, token, ws, study string) manualRow {
	t.Helper()
	var out manualRow
	body := map[string]any{"title": "A private choice", "context": "Think carefully", "deadline": "9999-12-31"}
	if study != "" {
		body["card_id"] = study
	}
	if code := s.call(t, "POST", "/api/decisions/manual", token, ws, body, &out); code != 201 {
		t.Fatalf("create manual: %d", code)
	}
	return out
}
func TestManualDecisionCreateEditResolveDismiss(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "manual@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "other@lab.org", "admin")
	for _, body := range []map[string]any{{"title": " "}, {"title": "a\nb"}, {"title": strings.Repeat("a", 201)}, {"title": "ok", "context": strings.Repeat("x", 4001)}, {"title": "ok", "deadline": "2026-02-30"}, {"title": "ok", "user_id": s.userID(t, b)}, {"title": "ok", "options": []any{}}} {
		if code := s.call(t, "POST", "/api/decisions/manual", a, w.ID, body, nil); code != 400 {
			t.Fatalf("invalid body %v: %d", body, code)
		}
	}
	item := s.createManual(t, a, w.ID, "")
	path := "/api/decisions/" + item.ID
	if code := s.call(t, "POST", path+"/resolve", a, w.ID, map[string]any{"outcome": "not allowed"}, nil); code != 400 {
		t.Fatalf("resolve outcome accepted: %d", code)
	}
	if code := s.call(t, "PATCH", path+"/manual", a, w.ID, map[string]any{"title": "ok", "user_id": s.userID(t, b)}, nil); code != 400 {
		t.Fatalf("creator edit accepted: %d", code)
	}
	if item.Title != "A private choice" || item.CanAnswer || !item.CanEdit || !item.CanResolve || item.UserID != s.userID(t, a) {
		t.Fatalf("created: %+v", item)
	}
	body := map[string]any{"title": "Changed", "context": "", "deadline": nil, "card_id": nil}
	if code := s.call(t, "PATCH", path+"/manual", b, w.ID, body, nil); code != 404 {
		t.Fatalf("foreign edit %d", code)
	}
	if code := s.call(t, "PATCH", path+"/manual", a, w.ID, body, &item); code != 200 || item.Title != "Changed" || item.Deadline != nil {
		t.Fatalf("edit: %d %+v", code, item)
	}
	if code := s.call(t, "POST", path+"/resolve", a, w.ID, nil, &item); code != 200 || item.Status != "decided" || item.CanEdit {
		t.Fatalf("resolve %d %+v", code, item)
	}
	if code := s.call(t, "PATCH", path+"/manual", a, w.ID, body, nil); code != 409 {
		t.Fatalf("terminal edit %d", code)
	}
	if got := s.decisions(t, a, w.ID, "?view=history"); len(got) != 1 {
		t.Fatalf("history %+v", got)
	}
	second := s.createManual(t, a, w.ID, "")
	if code := s.call(t, "POST", "/api/decisions/"+second.ID+"/dismiss", a, w.ID, nil, &item); code != 200 || item.Status != "dismissed" {
		t.Fatalf("dismiss %d %+v", code, item)
	}
}
func TestManualDecisionStudyViewerCanLink(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "owner@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "viewer@lab.org", "member")
	c := s.idleCard(t, a, w.ID, "study")
	item := s.createManual(t, b, w.ID, c.ID)
	outsider := s.signIn(t, "outside@lab.org")
	other := s.workspace(t, outsider, "other")
	foreign := s.idleCard(t, outsider, other.ID, "private")
	if code := s.call(t, "PATCH", "/api/decisions/"+item.ID+"/manual", b, w.ID, map[string]any{"title": "x", "card_id": foreign.ID}, nil); code != 404 {
		t.Fatalf("cross workspace study %d", code)
	}
}
func TestManualDecisionAlternateInboxRoutes(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "admin")
	item := s.createManual(t, a, w.ID, "")
	path := "/api/decisions/" + item.ID
	for _, action := range []string{"read", "keep", "archive", "revive", "resolve", "dismiss"} {
		if code := s.call(t, "POST", path+"/"+action, b, w.ID, nil, nil); code != 404 {
			t.Fatalf("foreign %s %d", action, code)
		}
	}
	for _, alternate := range []struct {
		action string
		body   map[string]any
	}{
		{"decide", map[string]any{"option": "park"}},
		{"answers", map[string]any{"answers": []any{map[string]any{"question_id": "q", "option_ids": []string{"park"}}}}},
	} {
		if code := s.call(t, "POST", path+"/"+alternate.action, b, w.ID, alternate.body, nil); code != 404 {
			t.Fatalf("foreign %s %d", alternate.action, code)
		}
		if code := s.call(t, "POST", path+"/"+alternate.action, a, w.ID, alternate.body, nil); code != 403 {
			t.Fatalf("manual note accepted %s: %d", alternate.action, code)
		}
	}
	snooze := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	triage := map[string]any{"decide_by": "today", "snoozed_until": snooze}
	if code := s.call(t, "PUT", path+"/triage", b, w.ID, triage, nil); code != 404 {
		t.Fatalf("foreign triage %d", code)
	}
	if code := s.call(t, "PUT", path+"/triage", a, w.ID, triage, nil); code != 204 {
		t.Fatalf("creator triage %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND decide_by='today' AND snoozed_until=$2 AND status='open' AND resolved_at IS NULL AND chosen_option='' AND answers IS NULL`, item.ID, snooze); n != 1 {
		t.Fatal("triage did not preserve open manual lifecycle")
	}
	if code := s.call(t, "POST", path+"/read", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("read %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE payload->>'inbox_item_id'=$1 AND recipient_id IS NULL`, item.ID); n != 0 {
		t.Fatalf("shared manual events %d", n)
	}
	for _, action := range []string{"archive", "revive", "keep"} {
		if code := s.call(t, "POST", path+"/"+action, a, w.ID, nil, nil); code != 204 {
			t.Fatalf("triage %s %d", action, code)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND status='open' AND resolved_at IS NULL`, item.ID); n != 1 {
		t.Fatal("triage closed note")
	}
}
func TestManualDecisionNeverChangesStudy(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "study")
	snapshot := func() string {
		var out string
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT row_to_json(c)::text FROM card c WHERE id=$1`, c.ID).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	events := s.count(t, `SELECT count(*) FROM event WHERE card_id=$1`, c.ID)
	item := s.createManual(t, a, w.ID, c.ID)
	path := "/api/decisions/" + item.ID
	if code := s.call(t, "PATCH", path+"/manual", a, w.ID, map[string]any{"title": "changed", "card_id": c.ID}, nil); code != 200 {
		t.Fatalf("edit %d", code)
	}
	if code := s.call(t, "POST", path+"/resolve", a, w.ID, nil, nil); code != 200 {
		t.Fatalf("resolve %d", code)
	}
	if snapshot() != before || s.count(t, `SELECT count(*) FROM event WHERE card_id=$1`, c.ID) != events || s.count(t, `SELECT count(*) FROM agent_run WHERE card_id=$1`, c.ID) != 0 {
		t.Fatal("manual actions changed study, events or runs")
	}
}

func TestManualDecisionConcurrentEditClose(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	item := s.createManual(t, a, w.ID, "")
	tx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), `UPDATE inbox_item SET status='decided',resolved_at=now() WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- s.call(t, "PATCH", "/api/decisions/"+item.ID+"/manual", a, w.ID, map[string]any{"title": "must not commit"}, nil)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		var query string
		err = s.Env.Owner.QueryRow(ctx, `SELECT COALESCE((SELECT query FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) LIMIT 1),'')`, tx.Conn().PgConn().PID()).Scan(&query)
		if err != nil {
			t.Fatal(err)
		}
		if query != "" {
			if !strings.Contains(query, "LockDecision") {
				t.Fatalf("edit blocked before reaching note lock: %s", query)
			}
			break
		}
		select {
		case code := <-done:
			t.Fatalf("edit completed before close released note lock: %d", code)
		case <-ctx.Done():
			t.Fatal("edit never reached the held note lock: ", ctx.Err())
		case <-tick.C:
		}
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 409 {
		t.Fatalf("edit racing committed close: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND title='A private choice' AND status='decided'`, item.ID); n != 1 {
		t.Fatal("terminal note edited")
	}
}
