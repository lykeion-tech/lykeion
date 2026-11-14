package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lykeion/server/pkg/agent"
)

// A message that shows the door's token, as `cat .lyk-mcp.json` or `env` in
// the agent's shell would, is stored with the token blanked out.
func TestTranscriptNeverHoldsTheDoorToken(t *testing.T) {
	f := fixture(t)
	var token string
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			token = o.DoorToken
			text, _ := json.Marshal("LYK_DOOR_TOKEN=" + o.DoorToken + "\nAuthorization: Bearer " + o.DoorToken)
			msgs := make(chan agent.Message, 2)
			msgs <- agent.Message{Type: "text", Content: text}
			msgs <- agent.Message{Type: "tool_result", Tool: "Bash", Content: json.RawMessage(`{"output":"` + o.DoorToken + `"}`)}
			close(msgs)
			done := make(chan agent.Result, 1)
			done <- agent.Result{SessionID: "s1", Output: "done", Usage: []agent.Usage{{Model: "m", InputTokens: 1, OutputTokens: 1}}}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { s := f.status(t); return s == "completed" || s == "failed" })
	stopDaemon(t, cancel, done)
	if len(token) < 16 {
		t.Fatalf("door token %d chars", len(token))
	}
	rows, err := f.env.Owner.Query(context.Background(), "SELECT content::text FROM agent_run_message WHERE agent_run_id=$1 ORDER BY seq", f.id)
	must(t, err)
	defer rows.Close()
	blanked := 0
	for rows.Next() {
		var s string
		must(t, rows.Scan(&s))
		if strings.Contains(s, token) {
			t.Fatal("a stored message holds the door token")
		}
		blanked += strings.Count(s, "[door token]")
	}
	must(t, rows.Err())
	if blanked != 3 {
		t.Fatalf("%d tokens blanked, want 3", blanked)
	}
}
