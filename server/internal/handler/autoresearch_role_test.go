package handler

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// runOnly creates a run_only autoresearch, runs it on the caller's daemon
// runtime, and returns the autoresearch, the run, the running agent run's token
// and a function that completes the agent run as the daemon would.
func (s *server) runOnly(t *testing.T, tok, wsID string) (apView, runView, string, func()) {
	t.Helper()
	personal := s.personalToken(t, tok)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{"workspace_id": wsID, "daemon_id": "d-ro", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register %d %+v", code, rts)
	}
	ap := s.autoresearch(t, tok, wsID, map[string]any{"title": "Digest", "description": "Summarise the week.", "execution_mode": "run_only"})
	var run runView
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/run", tok, wsID, nil, &run); code != 201 || run.Status != "running" || run.AgentRunID == nil {
		t.Fatalf("run: %d %+v", code, run)
	}
	ref := map[string]any{"workspace_id": wsID, "runtime_id": rts[0].ID}
	var claimed []struct {
		AgentRun struct {
			ID      string
			Attempt int32
		} `json:"agent_run"`
		Token string
	}
	if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", personal, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claimed); code != 200 {
		t.Fatalf("request: %d", code)
	}
	if len(claimed) != 1 || claimed[0].AgentRun.ID != *run.AgentRunID || claimed[0].AgentRun.Attempt <= 0 {
		t.Fatalf("claimed: %+v", claimed)
	}
	ref["attempt"] = claimed[0].AgentRun.Attempt
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+claimed[0].AgentRun.ID+"/start", personal, "", ref, nil); code != 204 {
		t.Fatalf("start %d", code)
	}
	complete := func() {
		t.Helper()
		if code := s.call(t, "POST", "/api/daemon/agent-runs/"+claimed[0].AgentRun.ID+"/complete", personal, "", ref, nil); code != 200 {
			t.Fatalf("complete %d", code)
		}
	}
	return ap, run, claimed[0].Token, complete
}

func TestTheReportIsStoredAndRaisesNoInboxItem(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	sub := s.member(t, a, w.ID, "s@lab.org", "member")
	gone := s.member(t, a, w.ID, "r@lab.org", "member")
	ap, run, ltk, _ := s.runOnly(t, a, w.ID)
	for _, tok := range []string{sub, gone} {
		if code := s.call(t, "PUT", "/api/autoresearch/"+ap.ID+"/subscribers/"+s.userID(t, tok), tok, w.ID, nil, nil); code != 204 {
			t.Fatalf("request: %d", code)
		}
	}
	if code := s.call(t, "DELETE", "/api/workspaces/"+w.ID+"/members/"+s.userID(t, gone), a, "", nil, nil); code != 204 {
		t.Fatalf("request: %d", code)
	}

	for _, bad := range []string{"", "a\x00b", strings.Repeat("x", 64<<10+1)} {
		if code := s.call(t, "POST", "/api/role/report", ltk, "", map[string]string{"text": bad}, nil); code != 400 {
			t.Fatalf("report %d bytes: %d", len(bad), code)
		}
	}
	if code := s.call(t, "POST", "/api/role/report", ltk, "", map[string]string{"text": "Two new papers on hepatic lipids."}, nil); code != 201 {
		t.Fatalf("report: %d", code)
	}
	if code := s.call(t, "POST", "/api/role/report", ltk, "", map[string]string{"text": "again"}, nil); code != 409 {
		t.Fatalf("a second report: %d", code)
	}
	var runs []runView
	if code := s.call(t, "GET", "/api/autoresearch/"+ap.ID+"/runs", a, w.ID, nil, &runs); code != 200 {
		t.Fatalf("request: %d", code)
	}
	if len(runs) != 1 || runs[0].ID != run.ID || runs[0].Report != "Two new papers on hepatic lipids." {
		t.Fatalf("%+v", runs)
	}
	if s.count(t, `SELECT count(*) FROM inbox_item WHERE autoresearch_run_id = $1`, run.ID) != 0 {
		t.Fatal("the report raised an Inbox item")
	}
}

func TestARunOnlyTokenWritesNothingButItsReport(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	other := s.idleCard(t, a, w.ID, "Someone's own question")
	ap, _, ltk, complete := s.runOnly(t, a, w.ID)

	if code := s.call(t, "POST", "/api/role/records", ltk, "", map[string]any{"kind": "question", "body": map[string]string{"text": "x"}}, nil); code != 403 {
		t.Fatalf("write a record: %d", code)
	}
	for _, p := range []string{"/comments", "/stop"} {
		if code := s.call(t, "POST", "/api/cards/"+other.ID+p, ltk, w.ID, map[string]string{"body": "x", "reason": "x", "to": "backlog"}, nil); code != 401 {
			t.Fatalf("%s with an agent run token: %d", p, code)
		}
	}

	complete()
	if code := s.call(t, "POST", "/api/role/report", ltk, "", map[string]string{"text": "late"}, nil); code != 401 {
		t.Fatalf("a finished agent run's token: %d", code)
	}
	var runs []runView
	if code := s.call(t, "GET", "/api/autoresearch/"+ap.ID+"/runs", a, w.ID, nil, &runs); code != 200 {
		t.Fatalf("request: %d", code)
	}
	if len(runs) != 1 || runs[0].Status != "completed" {
		t.Fatalf("the run did not follow its agent run: %+v", runs[0])
	}
}

func TestRunOnlyReportFullTextAndExcerpt(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap, run, token, _ := s.runOnly(t, a, w.ID)
	text := strings.Repeat("界", 2100)
	if code := s.call(t, "POST", "/api/role/report", token, "", map[string]string{"text": text}, nil); code != 201 {
		t.Fatal(code)
	}
	var runs []runView
	if code := s.call(t, "GET", "/api/autoresearch/"+ap.ID+"/runs", a, w.ID, nil, &runs); code != 200 {
		t.Fatal(code)
	}
	if len(runs) != 1 || runs[0].Report != text {
		t.Fatalf("full report: %+v", runs)
	}
	if s.count(t, `SELECT count(*) FROM inbox_item WHERE autoresearch_run_id=$1`, run.ID) != 0 {
		t.Fatal("a report raised an Inbox item")
	}
	if s.count(t, `SELECT count(*) FROM event WHERE type='autoresearch_run.reported' AND payload->>'run_id'=$1`, run.ID) != 1 {
		t.Fatal("missing report event")
	}
}

func TestRunOnlyScopeBoundariesAndCardInput(t *testing.T) {
	s := newServer(t)
	jwt, ws, _, stage := s.roleFixture(t)
	for _, path := range []string{"/api/role/report"} {
		if code := s.call(t, "POST", path, stage.token, "", map[string]string{"text": "x", "question": "x"}, nil); code != 403 {
			t.Fatalf("stage %s: %d", path, code)
		}
		if code := s.call(t, "POST", path, jwt, "", map[string]string{"text": "x", "question": "x"}, nil); code != 401 {
			t.Fatalf("person %s: %d", path, code)
		}
	}
	_, _, token, _ := s.runOnly(t, jwt, ws)
	for _, p := range []struct{ method, path string }{{"GET", "/api/role/records"}, {"POST", "/api/role/blobs"}, {"GET", "/api/role/blobs/" + strings.Repeat("a", 64)}} {
		if code := s.call(t, p.method, p.path, token, "", nil, nil); code != 403 {
			t.Fatalf("%s: %d", p.path, code)
		}
	}
}

// invalidateBody changes authority after middleware accepts it, before the
// handler can enter the transaction that writes the submitted body.
type invalidateBody struct {
	io.Reader
	invalidate func()
}

func (b *invalidateBody) Read(p []byte) (int, error) {
	if b.invalidate != nil {
		f := b.invalidate
		b.invalidate = nil
		f()
	}
	return b.Reader.Read(p)
}

func TestRunOnlyWritesRevalidateAfterBodyRead(t *testing.T) {
	for _, path := range []string{"/api/role/report"} {
		for _, action := range []string{"complete", "attempt", "revoke", "expire"} {
			t.Run(path+"/"+action, func(t *testing.T) {
				s := newServer(t)
				a := s.signIn(t, "a@lab.org")
				w := s.workspace(t, a, "lab")
				_, run, token, complete := s.runOnly(t, a, w.ID)
				body := &invalidateBody{Reader: strings.NewReader(`{"text":"late","question":"late"}`), invalidate: func() {
					if action == "complete" {
						complete()
						return
					}
					query := `DELETE FROM agent_run_token WHERE agent_run_id=$1 AND NOT ('call:record' = ANY(scopes))`
					if action == "attempt" {
						query = `UPDATE agent_run SET attempt=attempt+1 WHERE id=$1`
					}
					if action == "expire" {
						query = `UPDATE agent_run_token SET expires_at=now()-interval '1 minute' WHERE agent_run_id=$1 AND NOT ('call:record' = ANY(scopes))`
					}
					tag, err := s.Env.Owner.Exec(context.Background(), query, *run.AgentRunID)
					if err != nil || tag.RowsAffected() != 1 {
						t.Fatalf("invalidate: %v %v", tag, err)
					}
				}}
				req := httptest.NewRequest("POST", path, body)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				NewRouter(s.H).ServeHTTP(rec, req)
				if rec.Code != 401 {
					t.Fatalf("stale write: %d %s", rec.Code, rec.Body.String())
				}
				if s.count(t, `SELECT count(*) FROM event WHERE type IN ('autoresearch_run.reported','card.created') AND workspace_id=$1`, w.ID) != 0 {
					t.Fatal("stale write committed")
				}
			})
		}
	}
}

func TestRunOnlyConcurrentReportsCommitOnce(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, run, token, _ := s.runOnly(t, a, w.ID)
	first := s.roleRequestAsync("POST", "/api/role/report", token, "", strings.NewReader(`{"text":"first"}`))
	second := s.roleRequestAsync("POST", "/api/role/report", token, "", strings.NewReader(`{"text":"second"}`))
	x, y := awaitRoleRequest(t, first), awaitRoleRequest(t, second)
	if !((x.code == 201 && y.code == 409) || (x.code == 409 && y.code == 201)) {
		t.Fatalf("reports: %+v %+v", x, y)
	}
	if s.count(t, `SELECT count(*) FROM inbox_item WHERE autoresearch_run_id=$1`, run.ID) != 0 || s.count(t, `SELECT count(*) FROM event WHERE type='autoresearch_run.reported' AND payload->>'run_id'=$1`, run.ID) != 1 {
		t.Fatal("concurrent report duplicated notifications")
	}
}
