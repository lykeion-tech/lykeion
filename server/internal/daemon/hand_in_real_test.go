package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lykeion/server/pkg/agent"
)

const resumeSchema = `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}},"required":["a","b"],"additionalProperties":false}`

// A re-ask resumes the CLI's session with the same schema, and the resumed
// turn's answer still fits it. Each installed CLI, signed in as the person
// is, runs one sandboxed turn and one resumed turn. It spends real tokens,
// so it runs only on request.
func TestRealResumeKeepsSchema(t *testing.T) {
	if os.Getenv("LYK_REAL_CLI") != "1" {
		t.Skip("set LYK_REAL_CLI=1 to spend real tokens on two turns per installed CLI")
	}
	harnesses := Detect()
	if len(harnesses) == 0 {
		t.Skip("no agent CLI installed")
	}
	for harness, bin := range harnesses {
		t.Run(harness, func(t *testing.T) {
			l := newLykeion(t)
			base, err := os.UserHomeDir()
			must(t, err)
			home, err := os.MkdirTemp(base, ".lyk-resume-")
			must(t, err)
			t.Cleanup(func() { os.RemoveAll(home) })
			d := &daemon{cfg: Config{Home: home, StateDir: filepath.Join(home, "state"), Server: l.URL, Harnesses: map[string]string{harness: bin}, NewBackend: agent.New}}
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
			defer cancel()

			run := t.TempDir()
			path, err := runPath()
			must(t, err)
			private, err := d.privatePaths()
			must(t, err)
			opts := agent.ExecOptions{Dir: run, CacheDir: d.runCacheDir(""), Env: []string{"PATH=" + path}, OutputSchema: resumeSchema,
				OnMessage: func(agent.Message) error { return nil }}
			opts.Sandbox = d.cliSandbox(run, nil, false)
			door := &Door{Server: l.URL, Sources: d.sourcesURL(), RunToken: "ltk_run", CallToken: "ltk_call", Harness: harness,
				PrivatePaths: private, PrivateRoots: []string{home}, Child: func() agent.ExecOptions { return opts }}
			must(t, door.Start())
			defer door.Close()
			opts.DoorURL, opts.DoorToken = door.URL, door.Token
			opts.Env = append(opts.Env, "LYK_DOOR="+door.URL, "LYK_DOOR_TOKEN="+door.Token)
			backend, err := agent.New(harness, bin)
			must(t, err)
			turn := func(prompt string) agent.Result {
				t.Helper()
				sess, err := backend.Execute(ctx, prompt, opts)
				must(t, err)
				res := <-sess.Done
				if res.Err != nil {
					t.Fatalf("turn failed (resume %q): %v", opts.Resume, res.Err)
				}
				return res
			}
			check := func(output string, b int) {
				t.Helper()
				var got map[string]json.RawMessage
				if err := json.Unmarshal([]byte(output), &got); err != nil || len(got) != 2 || got["a"] == nil {
					t.Fatalf("answer %q does not fit the schema", output)
				}
				var gotB int
				if err := json.Unmarshal(got["b"], &gotB); err != nil || gotB != b {
					t.Fatalf("answer %q: b is not %d", output, b)
				}
			}
			first := turn(`Answer with field a set to "hello" and field b set to 1. Use no tools.`)
			check(first.Output, 1)
			if first.SessionID == "" {
				t.Fatal("the first turn named no session")
			}
			opts.Resume, opts.ClaudeBaseline = first.SessionID, first.ClaudeSnapshot
			// The prompt asks for prose: only a schema still binding gives JSON.
			second := turn("Change field b to 2, then tell me in one plain sentence what you changed. Use no tools.")
			check(second.Output, 2)
			t.Logf("%s: first %s, resumed %s", harness, first.Output, second.Output)
		})
	}
}
