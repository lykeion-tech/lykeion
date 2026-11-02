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
}
