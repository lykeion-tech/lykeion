package handler

import "testing"

func TestDeletingGeneratedBacklogCardSettlesRunAtomically(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "deletecard@lab.org")
	w := s.workspace(t, a, "lab")
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Delete me", "execution_mode": "create_card"})
	// Only an unassigned study waits in Backlog, where a card can be deleted.
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE autoresearch SET assignee_type = NULL, assignee_id = NULL`); err != nil {
		t.Fatal(err)
	}
	var run runView
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/run", a, w.ID, nil, &run); code != 201 || run.CardID == nil {
		t.Fatalf("run: %d %+v", code, run)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `ALTER TABLE event ADD CONSTRAINT refuse_deleted_run_event CHECK (type <> 'autoresearch_run.failed')`); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+*run.CardID, a, w.ID, nil, nil); code != 500 {
		t.Errorf("event rollback: %d", code)
	}
	if s.count(t, `SELECT count(*) FROM card WHERE id=$1`, *run.CardID) != 1 || s.count(t, `SELECT count(*) FROM autoresearch_run WHERE id=$1 AND status='running' AND card_id=$2`, run.ID, *run.CardID) != 1 {
		t.Fatal("partial delete after event failure")
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `ALTER TABLE event DROP CONSTRAINT refuse_deleted_run_event`); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+*run.CardID, a, w.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if s.count(t, `SELECT count(*) FROM autoresearch_run WHERE id=$1 AND status='failed' AND reason='card_deleted' AND card_id IS NULL`, run.ID) != 1 || s.count(t, `SELECT count(*) FROM event WHERE type='autoresearch_run.failed' AND payload->>'run_id'=$1 AND payload->>'reason'='card_deleted' AND payload->>'card_id'=$2`, run.ID, *run.CardID) != 1 {
		t.Fatal("missing terminal run history/event")
	}
	if err := s.H.Autoresearch.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.count(t, `SELECT count(*) FROM autoresearch_run WHERE id=$1 AND status='failed' AND reason='card_deleted'`, run.ID) != 1 {
		t.Fatal("sync changed final run")
	}
}

func TestRuleDeletionRefusesLiveRunOnlyAgents(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "deleterule@lab.org")
	w := s.workspace(t, a, "lab")
	ap, run, token, complete := s.runOnly(t, a, w.ID)
	for _, status := range []string{"queued", "dispatched", "running"} {
		if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status=$1 WHERE id=$2`, status, *run.AgentRunID); err != nil {
			t.Fatal(err)
		}
		before := s.count(t, `SELECT count(*) FROM event`)
		if code := s.call(t, "DELETE", "/api/autoresearch/"+ap.ID, a, w.ID, nil, nil); code != 409 {
			t.Fatalf("%s: %d", status, code)
		}
		if s.count(t, `SELECT count(*) FROM autoresearch WHERE id=$1`, ap.ID) != 1 || s.count(t, `SELECT count(*) FROM autoresearch_run WHERE id=$1`, run.ID) != 1 || s.count(t, `SELECT count(*) FROM event`) != before {
			t.Fatal("rejected delete mutated history")
		}
	}
	if code := s.call(t, "POST", "/api/role/report", token, "", map[string]string{"text": "Still reportable"}, nil); code != 201 {
		t.Fatalf("report after refusal: %d", code)
	}
	complete()
	if code := s.call(t, "DELETE", "/api/autoresearch/"+ap.ID, a, w.ID, nil, nil); code != 204 {
		t.Fatalf("settled delete: %d", code)
	}
	if s.count(t, `SELECT count(*) FROM agent_run WHERE id=$1 AND status='completed'`, *run.AgentRunID) != 1 {
		t.Fatal("settlement receipt removed")
	}
}

func TestReadingAutoresearchInboxResolvesAndArchives(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "inboxar@lab.org")
	w := s.workspace(t, a, "lab")
	other := s.member(t, a, w.ID, "otherinbox@lab.org", "member")
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Inbox card", "execution_mode": "create_card"})
	var run runView
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/run", a, w.ID, nil, &run); code != 201 {
		t.Fatal(code)
	}
	s.dropStudyRuns(t)
	_, _, token, _ := s.runOnly(t, a, w.ID)
	if code := s.call(t, "POST", "/api/role/report", token, "", map[string]string{"text": "Read this"}, nil); code != 201 {
		t.Fatal(code)
	}
	var items []struct{ ID, Kind string }
	if code := s.call(t, "GET", "/api/inbox", a, w.ID, nil, &items); code != 200 || len(items) != 2 {
		t.Fatalf("inbox %d %+v", code, items)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE inbox_item SET read_at='2000-01-01T00:00:00Z' WHERE id=$1`, items[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if code := s.call(t, "POST", "/api/inbox/"+item.ID+"/read", other, w.ID, nil, nil); code != 404 {
			t.Fatalf("other reader: %d", code)
		}
		if code := s.call(t, "POST", "/api/inbox/"+item.ID+"/read", a, w.ID, nil, nil); code != 204 {
			t.Fatal(code)
		}
		if s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND read_at IS NOT NULL AND resolved_at IS NOT NULL`, item.ID) != 1 || s.count(t, `SELECT count(*) FROM event WHERE type='inbox.resolved' AND payload->>'inbox_item_id'=$1`, item.ID) != 1 {
			t.Errorf("unresolved %s", item.Kind)
		}
		if code := s.call(t, "POST", "/api/inbox/"+item.ID+"/read", a, w.ID, nil, nil); code != 404 {
			t.Fatalf("repeat: %d", code)
		}
	}
	if s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND read_at='2000-01-01T00:00:00Z'`, items[0].ID) != 1 {
		t.Fatal("prior read timestamp changed")
	}
	var active, archived []map[string]any
	if code := s.call(t, "GET", "/api/inbox", a, w.ID, nil, &active); code != 200 || len(active) != 0 {
		t.Fatalf("active %d %+v", code, active)
	}
	if code := s.call(t, "GET", "/api/inbox?archived=1", a, w.ID, nil, &archived); code != 200 || len(archived) != 2 {
		t.Fatalf("archive %d %+v", code, archived)
	}
}

func TestRuleDeletionAfterCancellationPreservesLateSettlement(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "canceldelete@lab.org")
	w := s.workspace(t, a, "lab")
	ap, run, _, complete := s.runOnly(t, a, w.ID)
	if code := s.call(t, "POST", "/api/agent-runs/"+*run.AgentRunID+"/cancel", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/autoresearch/"+ap.ID, a, w.ID, nil, nil); code != 204 {
		t.Fatalf("delete cancelled: %d", code)
	}
	complete()
	if s.count(t, `SELECT count(*) FROM agent_run WHERE id=$1 AND status='cancelled'`, *run.AgentRunID) != 1 {
		t.Fatal("cancelled run receipt lost")
	}
}

// dropStudyRuns removes the agent runs an assigned study started, so a test of
// what happens to its card is not about a study with work in flight.
func (s *server) dropStudyRuns(t *testing.T) {
	t.Helper()
	if _, err := s.Env.Owner.Exec(t.Context(), `DELETE FROM agent_run WHERE card_id IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
}
