package handler

import (
	"strings"
	"testing"
)

func TestAutoresearchHandIn(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap, run, _, _ := s.runOnly(t, a, w.ID)
	personal := s.personalToken(t, a)
	var runtime string
	var attempt int32
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT runtime_id, attempt FROM agent_run WHERE id=$1`, *run.AgentRunID).Scan(&runtime, &attempt); err != nil {
		t.Fatal(err)
	}
	send := func(attempt int32, h map[string]any) handInOut {
		t.Helper()
		var out handInOut
		in := map[string]any{"workspace_id": w.ID, "runtime_id": runtime, "attempt": attempt, "hand_in": h}
		if c := s.call(t, "POST", "/api/daemon/agent-runs/"+*run.AgentRunID+"/hand-in", personal, "", in, &out); c != 200 {
			t.Fatalf("hand-in %d", c)
		}
		return out
	}
	hand := func(studies ...map[string]string) map[string]any {
		return map[string]any{"status": "done", "answer": "did it", "reason": nil, "report": "Two new papers.", "new_studies": studies}
	}
	cards := func() int {
		return s.count(t, `SELECT count(*) FROM card WHERE origin = $1`, "autoresearch:"+ap.ID)
	}
	reports := func() int {
		return s.count(t, `SELECT count(*) FROM inbox_item WHERE kind = 'autoresearch_report' AND autoresearch_run_id = $1`, run.ID)
	}

	long := map[string]string{"title": "Long", "question": strings.Repeat("q", 4001)}
	if out := send(attempt, hand(map[string]string{"title": "Good", "question": "Does X?"}, long)); out.Accepted || len(out.Problems) != 1 || !strings.Contains(out.Problems[0], "new_studies 2: question") {
		t.Fatalf("%+v", out)
	}
	if cards() != 0 || reports() != 0 {
		t.Fatal("a rejected hand-in wrote something")
	}

	ok := hand(map[string]string{"title": "X study", "question": "Does X?"}, map[string]string{"title": "x  STUDY", "question": "again"})
	if out := send(attempt, ok); !out.Accepted {
		t.Fatalf("%+v", out)
	}
	if cards() != 1 || reports() == 0 {
		t.Fatalf("cards %d reports %d", cards(), reports())
	}
	n := reports()

	// A retry on a later attempt applies nothing again.
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET attempt = attempt + 1 WHERE id = $1`, *run.AgentRunID); err != nil {
		t.Fatal(err)
	}
	if out := send(attempt+1, hand(map[string]string{"title": "Another", "question": "Does Y?"})); !out.Accepted {
		t.Fatalf("%+v", out)
	}
	if cards() != 1 || reports() != n {
		t.Fatalf("a repeat applied again: cards %d reports %d", cards(), reports())
	}

	// A study whose agent is gone is skipped and named in the report.
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET attempt = 1 WHERE id = $1`, *run.AgentRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE autoresearch_run SET report = '' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if tag, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent SET archived_at = now() WHERE workspace_id = $1`, w.ID); err != nil || tag.RowsAffected() == 0 {
		t.Fatal(tag, err)
	}
	if out := send(1, hand(map[string]string{"title": "No team", "question": "Does Q?"})); !out.Accepted {
		t.Fatalf("%+v", out)
	}
	var report string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT report FROM autoresearch_run WHERE id = $1`, run.ID).Scan(&report); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(report, "Two new papers.") || !strings.Contains(report, `Could not open the study "No team": `) || cards() != 1 {
		t.Fatalf("report %q, cards %d", report, cards())
	}
}

// A database failure while a study starts, after its card was created, is a
// server failure: nothing of the hand-in stays, and a retry applies it whole.
func TestAutoresearchHandInStartFailureLeavesNothing(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap, run, _, _ := s.runOnly(t, a, w.ID)
	personal := s.personalToken(t, a)
	var runtime string
	var attempt int32
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT runtime_id, attempt FROM agent_run WHERE id=$1`, *run.AgentRunID).Scan(&runtime, &attempt); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE FUNCTION fail_late() RETURNS trigger AS $$ BEGIN
			IF EXISTS (SELECT 1 FROM card WHERE id = NEW.card_id AND title = 'Fails late') THEN RAISE EXCEPTION 'boom'; END IF;
			RETURN NEW; END $$ LANGUAGE plpgsql`,
		`CREATE TRIGGER fail_late BEFORE INSERT ON agent_run FOR EACH ROW EXECUTE FUNCTION fail_late()`,
	} {
		if _, err := s.Env.Owner.Exec(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	in := map[string]any{"workspace_id": w.ID, "runtime_id": runtime, "attempt": attempt, "hand_in": map[string]any{
		"status": "done", "answer": "did it", "reason": nil, "report": "Two new papers.",
		"new_studies": []map[string]string{{"title": "Fine", "question": "Does X?"}, {"title": "Fails late", "question": "Does Y?"}}}}
	post := func() int {
		return s.call(t, "POST", "/api/daemon/agent-runs/"+*run.AgentRunID+"/hand-in", personal, "", in, nil)
	}
	count := func() int {
		return s.count(t, `SELECT count(*) FROM card WHERE origin = $1`, "autoresearch:"+ap.ID)
	}
	if c := post(); c < 500 || count() != 0 {
		t.Fatalf("status %d, cards %d", c, count())
	}
	if s.count(t, `SELECT count(*) FROM autoresearch_run WHERE id = $1 AND report = ''`, run.ID) != 1 {
		t.Fatal("a report was kept")
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `DROP TRIGGER fail_late ON agent_run`); err != nil {
		t.Fatal(err)
	}
	if c := post(); c != 200 || count() != 2 {
		t.Fatalf("retry: status %d, cards %d", c, count())
	}
}
