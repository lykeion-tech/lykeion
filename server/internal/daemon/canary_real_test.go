package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/handin"
	"lykeion/server/pkg/agent"
)

const canaryPrompt = `Search the sources door for "metformin colorectal cancer": call the lyk_door tool call_tool with server "sources", tool "search_literature" and args {"query": "metformin colorectal cancer"}. Then hand in what you found, or could_not_do with a reason; either is a valid hand-in.`

// Each installed CLI, signed in as the person is, passes its preflight and
// runs one sandboxed study-style turn under the Grounding hand-in schema: it
// reaches the sources door, uses nothing outside the fence, mentions no lyk
// command, and its final message goes through the daemon's hand-in step and
// parses as a Grounding hand-in without problems. It spends real
// tokens, so it runs only on request. The door's sources endpoint and call
// records are the in-process fake server, so the check needs no Lykeion server.
func TestCanary(t *testing.T) {
	if os.Getenv("LYK_CANARY") != "1" {
		t.Skip("set LYK_CANARY=1 to spend real tokens on a run per installed CLI")
	}
	harnesses := Detect()
	if len(harnesses) == 0 {
		t.Skip("no agent CLI installed")
	}
	for harness, bin := range harnesses {
		t.Run(harness, func(t *testing.T) {
			l := newLykeion(t)
			// The daemon's home sits under the person's, as a real one does,
			// with its state folder apart from the caches runs may write.
			base, err := os.UserHomeDir()
			must(t, err)
			home, err := os.MkdirTemp(base, ".lyk-canary-")
			must(t, err)
			t.Cleanup(func() { os.RemoveAll(home) })
			// The server accepts whatever hand-in is posted and keeps the last.
			var posted json.RawMessage
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					HandIn json.RawMessage `json:"hand_in"`
				}
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &body)
				posted = body.HandIn
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"accepted":true}`))
			}))
			defer hs.Close()
			d := &daemon{api: &apiclient.Client{Server: hs.URL, Token: "ltk_canary", HTTP: &http.Client{}}, cfg: Config{Home: home, StateDir: filepath.Join(home, "state"), Server: l.URL, Harnesses: map[string]string{harness: bin}, NewBackend: agent.New}}
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
			defer cancel()
			if reason, detail := d.preflight(ctx, harness); reason != "" {
				t.Fatalf("preflight failed: %s: %s", reason, detail)
			}

			run := t.TempDir()
			env, err := cliEnv(true, "", l.URL, run)
			must(t, err)
			private, err := d.privatePaths()
			must(t, err)
			var messages []agent.Message
			opts := agent.ExecOptions{Dir: run, CacheDir: d.runCacheDir(""), Env: env, OutputSchema: handin.Schema(handin.Grounding),
				OnMessage: func(m agent.Message) error { messages = append(messages, m); return nil }}
			opts.Sandbox = d.cliSandbox(run, nil, false)
			door := &Door{HandIn: true, Server: l.URL, Sources: d.sourcesURL(), RunToken: "ltk_run", CallToken: "ltk_call", Harness: harness,
				PrivatePaths: private, PrivateRoots: []string{home}, Child: func() agent.ExecOptions { return opts }}
			must(t, door.Start())
			defer door.Close()
			opts.DoorURL, opts.DoorToken = door.URL, door.Token
			opts.Env = append(opts.Env, doorEnv(true, harness, door)...)
			backend, err := agent.New(harness, bin)
			must(t, err)
			sess, err := backend.Execute(ctx, canaryPrompt, opts)
			must(t, err)
			res := <-sess.Done
			if res.Err != nil {
				t.Fatalf("run failed: %v", res.Err)
			}

			// The final answer goes through the daemon's hand-in step.
			var c claimed
			c.Token = "ltk_canary"
			c.AgentRun.ID, c.AgentRun.WorkspaceID, c.AgentRun.Kind = uuid.New(), uuid.New(), "role"
			must(t, os.MkdirAll(filepath.Join(home, scratchName(c.AgentRun.ID, c.AgentRun.Attempt)), 0o700))
			noReask := func(string, string) agent.Result {
				t.Fatal("the daemon asked for the hand-in to be corrected")
				return agent.Result{}
			}
			_, probs, err := d.handIn(ctx, c, res, noReask, func(agent.Message) error { return nil }, nil)
			must(t, err)
			if len(probs) > 0 {
				t.Errorf("hand-in refused: %v", probs)
			}
			if len(posted) == 0 {
				t.Error("the hand-in step posted nothing")
			}
			if _, probs := handin.Parse(handin.Grounding, []byte(res.Output)); len(probs) > 0 {
				t.Errorf("final output is not a valid Grounding hand-in: %v\n%.300q", probs, res.Output)
			}

			doorCalls := 0
			for _, m := range messages {
				if breach, what := agent.Fenced(harness, m, false); breach {
					t.Errorf("fence breach: %s", what)
				}
				if m.Type == "tool_use" && m.Server == "lyk_door" {
					doorCalls++
				}
				if c := string(m.Content); strings.Contains(c, "lyk ") {
					t.Errorf("message mentions a lyk command: %.120s", c)
				}
			}
			if doorCalls == 0 {
				t.Error("the run never called the sources door")
			}
			if strings.TrimSpace(res.Output) == "" {
				t.Error("no final message")
			}
			t.Logf("%s: %d messages, %d door calls, final: %.200q", harness, len(messages), doorCalls, res.Output)
		})
	}
}
