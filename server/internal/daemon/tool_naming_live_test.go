package daemon

import (
	"context"
	"encoding/json"
	"lykeion/server/pkg/agent"
	"os"
	"strings"
	"testing"
	"time"
)

// This acceptance test is opt-in because it spends real provider usage.
func TestLiveClaudeTranscriptNaming(t *testing.T) {
	bin, home := os.Getenv("LYK_LIVE_NAMING_CLAUDE"), os.Getenv("LYK_LIVE_NAMING_HOME")
	if bin == "" || home == "" {
		t.Skip("set LYK_LIVE_NAMING_CLAUDE and LYK_LIVE_NAMING_HOME for real provider acceptance")
	}
	f := fixture(t)
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f.cfg.Harnesses["claude"] = bin
	_, err := f.env.Owner.Exec(context.Background(), "UPDATE agent_run SET prompt=$2,skill=$3 WHERE id=$1", f.id, "Use the Read tool to read SKILL.md in your working directory. Then answer with one short sentence describing what you read. Do not run commands, change files, or access external databases.", "# Transcript acceptance\n\nThis disposable skill contains no scientific claims. Read it and report that you read it.")
	must(t, err)
	cancel, done := startDaemon(t, f.cfg)
	defer stopDaemon(t, cancel, done)
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	live := false
	for {
		select {
		case <-deadline.C:
			t.Fatal("live provider did not settle within two minutes")
		case <-tick.C:
			var count int
			must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND content->>'type'='tool_step_titles'", f.id).Scan(&count))
			status := f.status(t)
			live = live || count > 0 && status == "running"
			if status == "failed" {
				var reason string
				must(t, f.env.Owner.QueryRow(context.Background(), "SELECT error FROM agent_run WHERE id=$1", f.id).Scan(&reason))
				t.Fatalf("live run failed: %s", reason)
			}
			if status != "completed" {
				continue
			}
			if count == 0 {
				t.Fatal("real provider completed without recorded step titles")
			}
			rows, err := f.env.Owner.Query(context.Background(), "SELECT content FROM agent_run_message WHERE agent_run_id=$1 AND content->>'type'='tool_step_titles' ORDER BY seq", f.id)
			must(t, err)
			defer rows.Close()
			for rows.Next() {
				var body []byte
				must(t, rows.Scan(&body))
				meta, err := agent.ParseStepTitles(body)
				must(t, err)
				for _, title := range meta.Titles {
					if strings.TrimSpace(title.Title) == "" {
						t.Fatal("empty live title")
					}
					t.Logf("real provider title: %s", title.Title)
				}
			}
			must(t, rows.Err())
			var usage json.RawMessage
			must(t, f.env.Owner.QueryRow(context.Background(), "SELECT COALESCE(jsonb_agg(jsonb_build_object('model',model,'input_tokens',input_tokens,'output_tokens',output_tokens,'cache_read_tokens',cache_read_tokens,'cache_write_tokens',cache_write_tokens,'reported_cost_usd',cost_usd,'cost_reported',cost_reported)), '[]'::jsonb) FROM agent_run_usage WHERE agent_run_id=$1", f.id).Scan(&usage))
			t.Logf("settled research plus naming usage (provider cost is not a subscription invoice): %s", usage)
			t.Logf("title observed before settlement: %v; %d durable title records", live, count)
			return
		}
	}
}
