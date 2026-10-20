package daemon

import (
	"context"
	"encoding/json"
	"lykeion/server/pkg/agent"
	"os"
	"path/filepath"
	"testing"
)

func TestDaemonPersistsLiveToolNamesAndChargesBothConversations(t *testing.T) {
	f := fixture(t)
	bin := filepath.Join(t.TempDir(), "claude")
	must(t, os.WriteFile(bin, []byte(`#!/bin/sh
[ "$1" != --version ] || { echo '2.1.304';exit 0; }
previous=''
for option in "$@"; do
 if [ "$previous" = --tools ] && [ -z "$option" ];then
  while IFS= read -r prompt;do
   echo '{"type":"assistant","message":{"content":[{"type":"text","text":"1. Inspect the recorded study"}]}}'
   echo '{"type":"result","session_id":"naming-only","modelUsage":{"m":{"inputTokens":2,"outputTokens":2,"costUSD":0.001}}}'
  done
  exit 0
 fi
 previous=$option
done
case "$*" in *'--input-format stream-json'*) read prompt;;*)cat >/dev/null;;esac
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"read-study","name":"Read","input":{"file_path":"SKILL.md"}}]}}'
sleep 1
echo '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"read-study","content":"# Scoper"}]}}'
echo '{"type":"result","session_id":"research-main","modelUsage":{"m":{"inputTokens":5,"outputTokens":3,"costUSD":0.25}}}'
`), 0700))
	f.cfg.Harnesses["claude"] = bin
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	var body []byte
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT content FROM agent_run_message WHERE agent_run_id=$1 AND content->>'type'='tool_step_titles'", f.id).Scan(&body))
	meta, err := agent.ParseStepTitles(body)
	if err != nil || meta == nil || len(meta.Titles) != 1 || meta.Titles[0].Title != "Inspect the recorded study" || meta.Titles[0].ToolUseID != "read-study" {
		t.Fatalf("metadata %s: %v", body, err)
	}
	var cost float64
	var session string
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT cost_usd,session_id FROM agent_run WHERE id=$1", f.id).Scan(&cost, &session))
	if cost < .25099 || cost > .25101 || session != "research-main" {
		t.Fatalf("cost %v session %s", cost, session)
	}
	var original json.RawMessage
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT content FROM agent_run_message WHERE agent_run_id=$1 AND seq=$2", f.id, meta.Titles[0].Seq).Scan(&original))
	var call struct {
		Input struct {
			FilePath string `json:"file_path"`
		}
	}
	must(t, json.Unmarshal(original, &call))
	if call.Input.FilePath != "SKILL.md" {
		t.Fatalf("lost original %s", original)
	}
}
