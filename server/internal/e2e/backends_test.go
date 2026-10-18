package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/daemon"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type antigravityHeader string

func (a antigravityHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", string(a))
	return http.DefaultTransport.RoundTrip(req)
}

// The stand-in only captures the adapter boundary and holds the run open.
// Real MCP, lyk tools, HTTP handlers and PostgreSQL produce the evidence.
func TestAntigravityDoorRunProducesCallRecord(t *testing.T) {
	l := newBoardLab(t)
	t.Setenv("GEMINI_CLI_HOME", t.TempDir())
	ctx := context.Background()
	judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Questions map[string]struct{ Instructions string }
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		answers := map[string]any{}
		for id, q := range in.Questions {
			p := 0.0
			if strings.Contains(q.Instructions, "Workspace") || strings.Contains(q.Instructions, "door-method") || strings.Contains(q.Instructions, "Inspect recorded evidence.") {
				p = 0.9
			}
			answers[id] = map[string]any{"type": "noul", "noul": p}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": answers})
	}))
	defer judge.Close()
	l.engine.Jev = &jev.Client{BaseURL: judge.URL, Key: "test", Model: jev.Model}
	var sk struct {
		ID      string
		Content string
	}
	if code := l.api(t, "POST", "/api/skills", map[string]string{"name": "door-method", "description": "Inspect recorded evidence."}, &sk); code != 201 {
		t.Fatalf("create skill: %d", code)
	}
	if code := l.api(t, "PATCH", "/api/skills/"+sk.ID, map[string]any{"revision": 1, "content": sk.Content + "\n# Evidence\nInspect recorded evidence.\n"}, nil); code != 200 {
		t.Fatalf("edit skill: %d", code)
	}
	var card db.Card
	must(t, l.st.InWorkspace(ctx, l.ws, l.owner, func(tx *store.Tx) error {
		var err error
		card, err = tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: l.ws, Number: 1, Title: "Door evidence", Question: "X Y mice", OwnerID: l.owner, FollowsFrom: []uuid.UUID{}})
		return err
	}))
	capture := t.TempDir()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	bin := filepath.Join(t.TempDir(), "agy")
	session := "11111111-2222-3333-4444-555555555555"
	script := `#!/bin/sh
if [ "$1" = --version ]; then echo 'agy fixture'; exit 0; fi
capture=` + quote(capture) + `
cp .agents/mcp_config.json "$capture/mcp.tmp" || exit 41
printf '%s\n' "$@" > "$capture/args"
printf '%s\n' "$PWD" "$HOME" "$GEMINI_CLI_HOME" > "$capture/paths"
mv "$capture/mcp.tmp" "$capture/mcp"
while [ ! -f "$capture/release" ]; do sleep 0.05; done
echo '{"event":"result","result":{"conversation_id":"` + session + `","status":"SUCCESS","response":"done"}}'
`
	must(t, os.WriteFile(bin, []byte(script), 0700))
	cfg := daemon.Config{Server: l.url, Token: l.token, StateDir: t.TempDir(), Home: t.TempDir(), Device: "antigravity-test", Harnesses: map[string]string{"antigravity": bin}, Slots: 1, Poll: 50 * time.Millisecond, Heartbeat: time.Second, Flush: 50 * time.Millisecond}
	start := func() func() {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- daemon.Run(runCtx, cfg) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				select {
				case err := <-done:
					must(t, err)
				case <-time.After(10 * time.Second):
					t.Error("daemon shutdown did not join")
				}
			})
		}
		t.Cleanup(stop)
		return stop
	}
	var priorToken, priorDir string
	for turn := 0; turn < 2; turn++ {
		var run db.AgentRun
		must(t, l.st.InWorkspace(ctx, l.ws, l.owner, func(tx *store.Tx) error {
			var err error
			run, err = l.h.AgentRuns.Enqueue(ctx, tx, l.owner, service.NewAgentRun{Card: card.ID, Column: "grounding", Round: 1, Role: "agent", Harness: "antigravity", Prompt: "Inspect recorded evidence", MaxAttempts: 1, RunPrivate: true})
			return err
		}))
		if turn > 0 {
			_, err := l.env.Owner.Exec(ctx, `UPDATE agent_run SET session_id=$2 WHERE id=$1`, run.ID, session)
			must(t, err)
		}
		stop := start()
		status := func() string {
			var state, failure string
			must(t, l.env.Owner.QueryRow(ctx, `SELECT status, error FROM agent_run WHERE id=$1`, run.ID).Scan(&state, &failure))
			if state == "failed" {
				t.Fatalf("Antigravity failed: %s", failure)
			}
			return state
		}
		waitFor(t, "Antigravity to capture its door", func() bool { status(); _, err := os.Stat(filepath.Join(capture, "mcp")); return err == nil })
		read := func(name string) []byte { b, err := os.ReadFile(filepath.Join(capture, name)); must(t, err); return b }
		var config struct {
			MCPServers map[string]struct {
				ServerURL string            `json:"serverUrl"`
				Headers   map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		must(t, json.Unmarshal(read("mcp"), &config))
		door := config.MCPServers["lyk_door"]
		if len(config.MCPServers) != 1 || door.ServerURL == "" || door.Headers["Authorization"] == "" {
			t.Fatal("missing single recorded door")
		}
		paths := strings.Split(strings.TrimSpace(string(read("paths"))), "\n")
		if len(paths) != 3 || paths[1] != filepath.Join(paths[0], ".home") || paths[2] != filepath.Join(paths[0], ".cli-home") {
			t.Fatalf("non-private CLI homes: %v", paths)
		}
		if turn > 0 && (priorToken == door.Headers["Authorization"] || priorDir != paths[0] || !strings.Contains(string(read("args")), "--conversation\n"+session+"\n")) {
			t.Fatal("resume failed to reuse its scope with fresh credentials")
		}
		priorToken, priorDir = door.Headers["Authorization"], paths[0]
		callCtx, callCancel := context.WithTimeout(ctx, 20*time.Second)
		client := mcp.NewClient(&mcp.Implementation{Name: "antigravity-config-consumer", Version: "test"}, nil)
		conn, err := client.Connect(callCtx, &mcp.StreamableClientTransport{Endpoint: door.ServerURL, HTTPClient: &http.Client{Transport: antigravityHeader(door.Headers["Authorization"])}, DisableStandaloneSSE: true}, nil)
		must(t, err)
		list, err := conn.ListTools(callCtx, nil)
		must(t, err)
		if len(list.Tools) != 1 || list.Tools[0].Name != "call_tool" {
			t.Fatalf("unexpected MCP surface: %+v", list.Tools)
		}
		answer, err := conn.CallTool(callCtx, &mcp.CallToolParams{Name: "call_tool", Arguments: map[string]any{"server": "sources", "tool": "search_datasets", "args": map[string]any{"query": "X Y mice"}}})
		must(t, err)
		conn.Close()
		callCancel()
		if answer.IsError {
			t.Fatalf("recorded call failed: %+v", answer.Content)
		}
		var count int
		must(t, l.env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE workspace_id=$1 AND card_id=$2 AND agent_run_id=$3 AND kind='call' AND body->>'server'='sources' AND body->>'tool'='search_datasets' AND body->'args'->>'query'='X Y mice' AND body->>'answer_sha256' <> ''`, l.ws, card.ID, run.ID).Scan(&count))
		if count != 1 {
			t.Fatalf("run has %d recorded calls", count)
		}
		cmd := exec.Command("lyk", "tools", "Inspect recorded evidence.")
		cmd.Dir = paths[0]
		cmd.Env = append(os.Environ(), "LYK_DOOR="+door.ServerURL, "LYK_DOOR_TOKEN="+strings.TrimPrefix(door.Headers["Authorization"], "Bearer "), "HOME="+paths[1])
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "Inspect recorded evidence.") {
			t.Fatalf("lyk tools: %s %v", output, err)
		}
		for _, name := range []string{".agents/skills", "AGENTS.md", ".cli-home/skills", ".cli-home/.agents", ".home/.agents"} {
			if _, err := os.Lstat(filepath.Join(paths[0], name)); !os.IsNotExist(err) {
				t.Fatalf("lyk tools enabled private discovery %s: %v", name, err)
			}
		}
		must(t, os.WriteFile(filepath.Join(capture, "release"), nil, 0600))
		waitFor(t, "Antigravity completion", func() bool { return status() == "completed" })
		stop()
		if turn == 0 {
			// A reachable session is the daemon's prerequisite for passing resume.
			logs := filepath.Join(paths[2], ".gemini", "antigravity-cli", "brain", session, ".system_generated", "logs")
			must(t, os.MkdirAll(logs, 0700))
			must(t, os.WriteFile(filepath.Join(logs, "transcript.jsonl"), []byte("{}\n"), 0600))
			must(t, os.Remove(filepath.Join(capture, "mcp")))
			must(t, os.Remove(filepath.Join(capture, "release")))
		}
	}
}
