package handler

import "testing"

func TestAutoresearchRejectsInvalidEnvironmentEvidence(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "env-autoresearch@lab.org")
	w := s.workspace(t, a, "lab")
	_, run, _, _ := s.runOnly(t, a, w.ID)
	personal := s.personalToken(t, a)
	var runtime string
	var attempt int32
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT runtime_id, attempt FROM agent_run WHERE id=$1`, *run.AgentRunID).Scan(&runtime, &attempt); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"workspace_id": w.ID, "runtime_id": runtime, "attempt": attempt, "hand_in": map[string]any{"status": "done", "answer": "done", "reason": nil, "report": "report", "new_studies": []any{}}, "environment_failures": []string{"ruby"}}
	var out handInOut
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+*run.AgentRunID+"/hand-in", personal, "", in, &out); code != 200 || out.Accepted || len(out.Problems) == 0 {
		t.Fatalf("invalid evidence accepted: status=%d result=%+v", code, out)
	}
}
