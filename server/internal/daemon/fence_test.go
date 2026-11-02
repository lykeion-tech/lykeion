package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lykeion/server/pkg/agent"
)

func TestBreachFailsTheRun(t *testing.T) {
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
	waitFor(t, func() bool { s := f.status(t); return s == "failed" || s == "completed" })
	stopDaemon(t, cancel, done)
	var status, reason, text string
	must(t, f.env.Owner.QueryRow(t.Context(), "SELECT status, coalesce(failure_reason, ''), error FROM agent_run WHERE id=$1", f.id).Scan(&status, &reason, &text))
	if status != "failed" || reason != agent.ReasonFenceBreach || text != "used WebFetch, which is outside Lykeion's sources" {
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
