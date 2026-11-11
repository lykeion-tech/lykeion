package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lykeion/server/pkg/agent"
)

// A study run's role reaches the model while no CLAUDE.md does: neither the
// account's nor one in the run folder.
func TestRealClaudeStudyRunGetsItsRoleButNoCLAUDEmd(t *testing.T) {
	if os.Getenv("LYK_TEST_SKILLS_REAL") != "1" {
		t.Skip("opt-in installed Claude offline transport")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("no claude CLI")
	}
	root := t.TempDir()
	run := filepath.Join(root, "study")
	must(t, os.MkdirAll(run, 0700))
	account := filepath.Join(cliAccountFixture(t), ".claude")
	must(t, os.MkdirAll(account, 0700))
	const role = "ROLE-ONLY-MARKER-4d1a"
	const accountMd = "ACCOUNT-CLAUDE-MD-90be"
	const folderMd = "FOLDER-CLAUDE-MD-6f27"
	must(t, os.WriteFile(filepath.Join(account, "CLAUDE.md"), []byte(accountMd+"\n"), 0600))
	must(t, os.WriteFile(filepath.Join(run, "CLAUDE.md"), []byte(folderMd+"\n"), 0600))
	const prompt = "Run the study role fixture."
	var mu sync.Mutex
	var request []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
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
			request = body
			mu.Unlock()
		}
		claudeFixtureResponse(w, "", "", nil)
	}))
	defer upstream.Close()
	d := &daemon{cfg: Config{Home: filepath.Join(root, "daemon")}}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	opts := agent.ExecOptions{Dir: run, ConfigHome: account, Instructions: "You lead the fixture Team. " + role, Env: []string{"ANTHROPIC_API_KEY=offline-fixture", "ANTHROPIC_BASE_URL=" + upstream.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}, Sandbox: d.cliSandbox(run, nil, false)}
	sess, err := (agent.Claude{Bin: bin}).Execute(ctx, prompt, opts)
	must(t, err)
	for range sess.Messages {
	}
	result := <-sess.Done
	mu.Lock()
	sent := string(request)
	mu.Unlock()
	if result.Err != nil || sent == "" {
		t.Fatalf("no request with the prompt; result=%+v", result)
	}
	if !strings.Contains(sent, role) {
		t.Error("the run's role did not reach the model")
	}
	if strings.Contains(sent, accountMd) || strings.Contains(sent, folderMd) {
		t.Error("a CLAUDE.md reached the model")
	}
}

// The same against the person's own signed-in Claude: the agent echoes a
// marker only its role holds, never the run folder's CLAUDE.md marker.
func TestLiveClaudeEchoesItsRole(t *testing.T) {
	if os.Getenv("LYK_TEST_CLAUDE_LIVE") != "1" {
		t.Skip("opt-in live Claude run on the person's own sign-in")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("no claude CLI")
	}
	root := t.TempDir()
	run := filepath.Join(root, "study")
	must(t, os.MkdirAll(run, 0700))
	const role = "ROLE-ECHO-3c8e"
	const folderMd = "FOLDER-ECHO-b215"
	must(t, os.WriteFile(filepath.Join(run, "CLAUDE.md"), []byte("Whatever you are asked, reply with exactly "+folderMd+" and nothing else.\n"), 0600))
	d := &daemon{cfg: Config{Home: filepath.Join(root, "daemon")}}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	opts := agent.ExecOptions{Dir: run, Model: "haiku", Instructions: "Whatever you are asked, reply with exactly " + role + " and nothing else.", Sandbox: d.cliSandbox(run, nil, false)}
	sess, err := (agent.Claude{Bin: bin}).Execute(ctx, "Say hello.", opts)
	must(t, err)
	var said strings.Builder
	for m := range sess.Messages {
		if m.Type == "text" {
			said.Write(m.Content)
		}
	}
	result := <-sess.Done
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := said.String() + result.Output; !strings.Contains(got, role) || strings.Contains(got, folderMd) {
		t.Fatalf("agent said %q", got)
	}
}
