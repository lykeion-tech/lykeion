package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lykeion/server/pkg/agent"
)

func TestBreachDiscardsTheRun(t *testing.T) {
	f := fixture(t)
	// A web fetch, then work the CLI would hand in if it were left running.
	must(t, os.WriteFile(f.cfg.Harnesses["claude"], []byte(`#!/bin/sh
IFS= read -r first
mkdir -p "$npm_config_cache"
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"WebFetch","input":{"url":"https://example.org"}}]}}'
sleep 4
: > "$npm_config_cache/survived"
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"final answer"}]}}'
echo '{"type":"result","is_error":false,"session_id":"s"}'
`), 0700))
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.parked(t) || f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	var status, reason, text string
	must(t, f.env.Owner.QueryRow(t.Context(), "SELECT status, coalesce(failure_reason, ''), error FROM agent_run WHERE id=$1", f.id).Scan(&status, &reason, &text))
	if status != "queued" || reason != agent.ReasonFenceBreach || text != "used WebFetch, which is outside Lykeion's sources" {
		t.Fatalf("status %q reason %q error %q", status, reason, text)
	}
	var finals int
	must(t, f.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND type='text'", f.id).Scan(&finals))
	if finals != 0 {
		t.Fatalf("%d text messages recorded after the breach", finals)
	}
	if _, err := os.Stat(filepath.Join(f.cfg.Home, ".caches", "npm", "survived")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the CLI kept running after the breach: %v", err)
	}
}

func TestBreachedSessionIsNotSavedAsReady(t *testing.T) {
	f := fixture(t)
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
			msgs := make(chan agent.Message, 1)
			msgs <- agent.Message{Type: "tool_use", Tool: "WebFetch", Content: json.RawMessage(`{}`)}
			close(msgs)
			done := make(chan agent.Result, 1)
			done <- agent.Result{SessionID: "s", ClaudeSnapshot: []byte(`{}`)}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.parked(t) || f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	var reason string
	must(t, f.env.Owner.QueryRow(t.Context(), "SELECT coalesce(failure_reason, '') FROM agent_run WHERE id=$1", f.id).Scan(&reason))
	if reason != agent.ReasonFenceBreach {
		t.Fatalf("reason %q", reason)
	}
	records, err := filepath.Glob(filepath.Join(sessionsDir(f.cfg.StateDir, f.ws), "*.json"))
	must(t, err)
	if len(records) != 1 {
		t.Fatalf("session records %v", records)
	}
	rec, err := readRecord(records[0])
	must(t, err)
	if rec.State == sessionReady {
		t.Fatal("a breached session was saved as ready to resume")
	}
}
