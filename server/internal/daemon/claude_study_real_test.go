package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"lykeion/server/pkg/agent"
)

// A study run starts and resumes against an offline provider while the
// account's instructions, customizations, hooks and MCP servers exist, and
// none of them reach the model or run.
func TestRealClaudeStudyRunResumesWithoutPersonalContext(t *testing.T) {
	if os.Getenv("LYK_TEST_SKILLS_REAL") != "1" {
		t.Skip("opt-in installed Claude offline transport")
	}
	bin, err := exec.LookPath("claude")
	must(t, err)
	root := t.TempDir()
	run := filepath.Join(root, "study")
	must(t, os.MkdirAll(run, 0700))
	account := filepath.Join(cliAccountFixture(t), ".claude")
	const personal = "USER-PERSONAL-CONTEXT-5be1"
	for _, dir := range []string{"skills/mine", "plugins/cache", "agents", "commands", "rules"} {
		must(t, os.MkdirAll(filepath.Join(account, dir), 0700))
	}
	for _, file := range []string{"CLAUDE.md", "agents/mine.md", "commands/mine.md", "rules/mine.md", "history.jsonl"} {
		must(t, os.WriteFile(filepath.Join(account, file), []byte(personal+"\n"), 0600))
	}
	must(t, os.WriteFile(filepath.Join(account, "skills/mine/SKILL.md"), []byte("---\nname: mine\ndescription: "+personal+"\n---\n"+personal+"\n"), 0600))
	mcpMarker := filepath.Join(root, "personal-mcp-started")
	hookMarker := filepath.Join(root, "personal-hook-ran")
	must(t, os.WriteFile(filepath.Join(account, ".claude.json"), []byte(`{"mcpServers":{"personal":{"type":"stdio","command":"/usr/bin/touch","args":["`+mcpMarker+`"]}}}`), 0600))
	must(t, os.WriteFile(filepath.Join(account, "settings.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/bin/touch `+hookMarker+`"}]}]}}`), 0600))
	const prompt = "Run the study isolation fixture."
	var mu sync.Mutex
	var requests [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"input_tokens":1}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(string(body), prompt) {
			mu.Lock()
			requests = append(requests, slices.Clone(body))
			mu.Unlock()
		}
		claudeFixtureResponse(w, "", "", nil)
	}))
	defer upstream.Close()
	d := &daemon{cfg: Config{Home: filepath.Join(root, "daemon")}}
	var resume string
	for turn := 0; turn < 2; turn++ {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		opts := agent.ExecOptions{Dir: run, ConfigHome: account, Resume: resume, Env: []string{"ANTHROPIC_API_KEY=offline-fixture", "ANTHROPIC_BASE_URL=" + upstream.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}, Sandbox: d.cliSandbox(run, nil, false)}
		sess, err := (agent.Claude{Bin: bin}).Execute(ctx, prompt, opts)
		must(t, err)
		var logs strings.Builder
		for m := range sess.Messages {
			logs.Write(m.Content)
		}
		result := <-sess.Done
		cancel()
		mu.Lock()
		captured := slices.Clone(requests)
		mu.Unlock()
		if result.Err != nil || len(captured) != turn+1 {
			t.Fatalf("turn %d: requests=%d result=%+v logs=%s", turn, len(captured), result, logs.String())
		}
		last := string(captured[turn])
		if strings.Contains(last, personal) {
			t.Fatalf("turn %d: the account's personal context reached the model", turn)
		}
		// A resumed turn carries the first turn's exchange.
		if turn == 1 && strings.Count(last, prompt) < 2 {
			t.Fatal("turn 1 did not resume the first turn's session")
		}
		for _, marker := range []string{mcpMarker, hookMarker} {
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("turn %d: %s", turn, filepath.Base(marker))
			}
		}
		if result.SessionID == "" {
			t.Fatal("no persisted session for resume")
		}
		resume = result.SessionID
		t.Logf("turn %d: offline request without personal context; resumed=%v", turn, turn == 1)
	}
}
