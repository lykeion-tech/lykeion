package e2e

import (
	"bytes"
	"context"
	"encoding/json"

	"lykeion/server/internal/skills"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (l *boardLab) api(t *testing.T, method, path string, in, out any) int {
	t.Helper()
	var body *bytes.Reader
	if in != nil {
		b, err := json.Marshal(in)
		must(t, err)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, l.url+path, body)
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+l.token)
	req.Header.Set("X-Workspace-ID", l.ws.String())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		must(t, json.NewDecoder(resp.Body).Decode(out))
	}
	return resp.StatusCode
}

func TestRunOnlyThroughADaemonHasOnlyItsTwoPowers(t *testing.T) {
	l := newBoardLab(t)
	capture := filepath.Join(l.home, ".caches", "capture")
	must(t, os.MkdirAll(capture, 0700))
	t.Setenv("LYK_TEST_CAPTURE", capture)
	ctx := context.Background()
	if _, err := l.env.Owner.Exec(ctx, `INSERT INTO stage_addon (workspace_id, board_column, skill_md, mcp, updated_by)
		VALUES ($1, 'grounding', 'Grounding roles only.', '[{"name":"lab-index","url":"http://127.0.0.1:9/mcp"}]', $2)`, l.ws, l.owner); err != nil {
		t.Fatal(err)
	}
	defer l.run(t)()
	waitFor(t, "the daemon's runtime", func() bool {
		var n int
		must(t, l.env.Owner.QueryRow(ctx, `SELECT count(*) FROM runtime WHERE status = 'online' AND harness = 'claude'`).Scan(&n))
		return n > 0
	})
	var ap struct{ ID string }
	var agent struct{ ID string }
	if code := l.api(t, "POST", "/api/agents", map[string]any{"name": "digester", "harness": "claude"}, &agent); code != 201 {
		t.Fatalf("agent: %d", code)
	}
	if code := l.api(t, "POST", "/api/autoresearch", map[string]any{"title": "Weekly digest", "description": "Summarise what changed.", "execution_mode": "run_only",
		"assignee": map[string]any{"type": "agent", "id": agent.ID}}, &ap); code != 201 || ap.ID == "" {
		t.Fatalf("create: %d %+v", code, ap)
	}
	var run struct{ ID, Status string }
	if code := l.api(t, "POST", "/api/autoresearch/"+ap.ID+"/run", nil, &run); code != 201 || run.Status != "running" {
		t.Fatalf("run now: %d %+v", code, run)
	}
	waitFor(t, "the run to complete", func() bool {
		var s string
		must(t, l.env.Owner.QueryRow(ctx, `SELECT status FROM autoresearch_run WHERE id = $1`, run.ID).Scan(&s))
		if s == "failed" {
			var detail string
			must(t, l.env.Owner.QueryRow(ctx, `SELECT error FROM agent_run WHERE id=(SELECT agent_run_id FROM autoresearch_run WHERE id=$1)`, run.ID).Scan(&detail))
			t.Fatalf("autoresearch failed: %s", detail)
		}
		return s == "completed"
	})

	var scope, profile string
	must(t, l.env.Owner.QueryRow(ctx, `SELECT continuity_scope, profile_mode FROM agent_run WHERE id = (SELECT agent_run_id FROM autoresearch_run WHERE id = $1)`, run.ID).Scan(&scope, &profile))
	if scope != "" || profile != "sandboxed" {
		t.Fatalf("run mode: scope=%q profile=%q", scope, profile)
	}
	var report string
	var items int
	must(t, l.env.Owner.QueryRow(ctx, `SELECT report FROM autoresearch_run WHERE id = $1`, run.ID).Scan(&report))
	must(t, l.env.Owner.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE kind = 'autoresearch_report' AND user_id = $1`, l.owner).Scan(&items))
	if report != "Nothing new this week." || items != 1 {
		t.Fatalf("report %q, Inbox items %d", report, items)
	}
	var owner, origin string
	if err := l.env.Owner.QueryRow(ctx, `SELECT owner_id::text, origin FROM card WHERE question = 'Does Z lower W in mouse liver?'`).Scan(&owner, &origin); err != nil ||
		owner != l.owner.String() || origin != "autoresearch:"+ap.ID {
		t.Fatalf("the card the run opened: %s %s %v", owner, origin, err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(capture, name))
		if err != nil {
			t.Fatalf("%s was not captured: %v", name, err)
		}
		return string(b)
	}
	if r := read("autoresearch.refused"); !strings.Contains(r, "POST /api/role/records: this agent run's token may not do that") || strings.Contains(r, "ALLOWED") {
		t.Fatalf("the role command was not refused: %q", r)
	}
	// the CLI is given the daemon's tool door alone; add-on servers sit behind it
	if cfg := read("autoresearch.mcp"); !strings.Contains(cfg, `"lyk_door"`) || strings.Contains(cfg, "lab-index") || strings.Contains(cfg, "lyk_sources") {
		t.Fatalf("MCP config: %s", cfg)
	}
	if s := read("autoresearch.skill"); s != skills.Role("autoresearch", "run-only") || strings.Contains(s, "Grounding roles only.") {
		t.Fatalf("skill:\n%s", s)
	}
	if a := read("autoresearch.args"); strings.Contains(a, "\nplan\n") {
		t.Fatalf("the agent run ran read-only: %s", a)
	}
	// a manual run_only run streams its input, never resumes and loads only its own MCP config
	if a := read("autoresearch.args"); !strings.Contains(a, "--input-format\nstream-json") || strings.Contains(a, "--resume") ||
		!strings.Contains(a, "--strict-mcp-config") {
		t.Fatalf("the agent run lost its stream input or isolation: %s", a)
	}
}
