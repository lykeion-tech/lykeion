package handler

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
)

// autoresearchAgentRun enqueues a card-less agent run for the caller, claims and starts it
// through the daemon routes with the caller's personal token, and returns its token.
func (s *server) autoresearchAgentRun(t *testing.T, tok, wsID string) string {
	t.Helper()
	personal := s.personalToken(t, tok)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{"workspace_id": wsID, "daemon_id": "d-ap", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register %d %+v", code, rts)
	}
	ws, u := uuid.MustParse(wsID), uuid.MustParse(s.userID(t, tok))
	err := s.H.Store.InWorkspace(context.Background(), ws, u, func(tx *store.Tx) error {
		_, err := s.H.AgentRuns.EnqueueAutoresearch(context.Background(), tx, u, service.AutoresearchAgentRun{Harness: "claude", Prompt: "p"})
		return err
	})
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("claim: %d", code)
	}
	if len(claimed) != 1 || claimed[0].AgentRun.Attempt <= 0 {
		t.Fatalf("claimed %+v", claimed)
	}
	ref["attempt"] = claimed[0].AgentRun.Attempt
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+claimed[0].AgentRun.ID+"/start", personal, "", ref, nil); code != 204 {
		t.Fatalf("start %d", code)
	}
	return claimed[0].Token
}

func TestAnAutoresearchTokenCannotActAsARole(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ltk := s.autoresearchAgentRun(t, a, w.ID)
	if code := s.call(t, "POST", "/api/role/records", ltk, "", map[string]any{"kind": "question", "body": map[string]string{"text": "x"}}, nil); code != 403 {
		t.Fatalf("write a record: %d", code)
	}
	if code := s.call(t, "GET", "/api/role/records", ltk, "", nil, nil); code != 403 {
		t.Fatalf("read records: %d", code)
	}
	req, err := http.NewRequest("POST", s.URL+"/api/role/blobs", bytes.NewReader([]byte("text")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ltk)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("store a blob: %d", resp.StatusCode)
	}
}
