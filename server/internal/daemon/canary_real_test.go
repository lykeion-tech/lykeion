package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/pkg/agent"
)

const canaryPrompt = `Search the sources door for "metformin colorectal cancer": call the lyk_door tool call_tool with server "sources", tool "search_literature" and args {"query": "metformin colorectal cancer"}. Then reply with one DOI from the results, or say none was listed.`

// Each installed CLI, signed in as the person is, passes its preflight and
// runs one sandboxed study-style turn: it reaches the sources door, uses
// nothing outside the fence, and ends with a final message. It spends real
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
			d := &daemon{cfg: Config{Home: home, StateDir: filepath.Join(home, "state"), Server: l.URL, Harnesses: map[string]string{harness: bin}, NewBackend: agent.New}}
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
			defer cancel()
			if reason, detail := d.preflight(ctx, harness); reason != "" {
				t.Fatalf("preflight failed: %s: %s", reason, detail)
			}

			run := t.TempDir()
			path, err := runPath()
			must(t, err)
			private, err := d.privatePaths()
			must(t, err)
			var messages []agent.Message
			opts := agent.ExecOptions{Dir: run, CacheDir: d.runCacheDir(""), Env: []string{"PATH=" + path},
				OnMessage: func(m agent.Message) error { messages = append(messages, m); return nil }}
			opts.Sandbox = d.cliSandbox(run, nil, false)
			door := &Door{Server: l.URL, Sources: d.sourcesURL(), RunToken: "ltk_run", CallToken: "ltk_call", Harness: harness,
				PrivatePaths: private, PrivateRoots: []string{home}, Child: func() agent.ExecOptions { return opts }}
			must(t, door.Start())
			defer door.Close()
			opts.DoorURL, opts.DoorToken = door.URL, door.Token
			opts.Env = append(opts.Env, "LYK_DOOR="+door.URL, "LYK_DOOR_TOKEN="+door.Token)
			backend, err := agent.New(harness, bin)
			must(t, err)
			sess, err := backend.Execute(ctx, canaryPrompt, opts)
			must(t, err)
			res := <-sess.Done
			if res.Err != nil {
				t.Fatalf("run failed: %v", res.Err)
			}

			doorCalls := 0
			for _, m := range messages {
				if breach, what := agent.Fenced(harness, m); breach {
					t.Errorf("fence breach: %s", what)
				}
				if m.Type == "tool_use" && m.Server == "lyk_door" {
					doorCalls++
				}
				// Informational until the hand-in replaces the record and team commands.
				if c := string(m.Content); strings.Contains(c, "lyk record") || strings.Contains(c, "lyk team") {
					t.Logf("message mentions the retired protocol: %.120s", c)
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
