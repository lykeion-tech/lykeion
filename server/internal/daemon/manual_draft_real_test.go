package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/labmemory"
	"lykeion/server/pkg/agent"
)

// This opt-in check uses installed, signed-in CLIs and real provider tokens.
// The daemon, scratch folders and hand-in receiver are disposable; the normal
// lab sandbox, read-only adapter and personal-context fence stay enabled.
func TestManualDraftRealCLI(t *testing.T) {
	if os.Getenv("LYK_MANUAL_DRAFT_REAL") != "1" {
		t.Skip("set LYK_MANUAL_DRAFT_REAL=1 to spend real tokens on private drafting checks")
	}
	installed := Detect()
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			bin, ok := installed[harness]
			if !ok {
				t.Skip("CLI not installed")
			}
			l := newLykeion(t)
			base, err := os.UserHomeDir()
			must(t, err)
			home, err := os.MkdirTemp(base, ".lyk-manual-acceptance-")
			must(t, err)
			t.Cleanup(func() { os.RemoveAll(home) })
			posted := make(chan json.RawMessage, 1)
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					HandIn json.RawMessage `json:"hand_in"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid envelope", 400)
					return
				}
				_, problems := handin.Parse(handin.ManualDecisionDraft, body.HandIn)
				w.Header().Set("Content-Type", "application/json")
				if len(problems) > 0 {
					_ = json.NewEncoder(w).Encode(map[string]any{"accepted": false, "problems": problems})
					return
				}
				posted <- body.HandIn
				_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true})
			}))
			defer receiver.Close()
			d := &daemon{api: &apiclient.Client{Server: receiver.URL, Token: "ltk_disposable", HTTP: &http.Client{}}, cfg: Config{Home: home, StateDir: filepath.Join(home, "state"), Server: l.URL, Harnesses: map[string]string{harness: bin}, NewBackend: agent.New}}
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
			defer cancel()
			if reason, detail := d.preflight(ctx, harness); reason != "" {
				t.Fatalf("provider preflight unavailable: %s: %s", reason, detail)
			}
			run := t.TempDir()
			env, err := cliEnv(true, "", l.URL, run)
			must(t, err)
			private, err := d.privatePaths()
			must(t, err)
			var messages []agent.Message
			opts := agent.ExecOptions{Lab: true, ReadOnly: true, Dir: run, CacheDir: d.runCacheDir(""), Env: env, OutputSchema: handin.Schema(handin.ManualDecisionDraft), OnMessage: func(m agent.Message) error { messages = append(messages, m); return nil }}
			opts.Sandbox = d.cliSandbox(run, nil, true)
			door := &Door{Lab: true, labURL: d.labURL(), HandIn: true, Server: l.URL, Sources: d.sourcesURL(), RunToken: "ltk_disposable_run", CallToken: "ltk_disposable_call", Harness: harness, PrivatePaths: private, PrivateRoots: []string{home}, SandboxErr: d.sandboxErr, Child: func() agent.ExecOptions { return opts }}
			must(t, door.Start())
			defer door.Close()
			opts.DoorURL, opts.DoorToken = door.URL, door.Token
			opts.Env = append(opts.Env, doorEnv(true, harness, door)...)
			backend, err := agent.New(harness, bin)
			must(t, err)
			prompt := labmemory.ManualDraftProtocol() + "\n" + `{"user_request":"Propose a concise title for this private reminder. This is only a proposal; make no factual supporting claims and return an empty statements array. No lab evidence or tools are needed for this wording task. Preserve context, deadline and card_id exactly.","current_draft":{"title":"Review the controls","context":"Check the assay controls before planning the next run.","deadline":null,"card_id":null}}`
			session, err := backend.Execute(ctx, prompt, opts)
			must(t, err)
			result := <-session.Done
			if result.Err != nil {
				t.Fatalf("sandboxed draft failed: %v", result.Err)
			}
			parsed, problems := handin.Parse(handin.ManualDecisionDraft, []byte(result.Output))
			if len(problems) > 0 {
				t.Fatalf("invalid draft hand-in: %v", problems)
			}
			if parsed.Draft == nil || parsed.Draft.Context != "Check the assay controls before planning the next run." || parsed.Draft.Deadline != nil || parsed.Draft.CardID != nil || len(parsed.Statements) != 0 {
				t.Fatalf("proposal did not preserve supplied draft: %+v", parsed.Draft)
			}
			for _, m := range messages {
				if breach, what := agent.Fenced(harness, m); breach {
					t.Fatalf("lab fence breach: %s", what)
				}
			}
			var claim claimed
			claim.Token = "ltk_disposable"
			claim.AgentRun.ID, claim.AgentRun.WorkspaceID, claim.AgentRun.Kind = uuid.New(), uuid.New(), "aristotle"
			noReask := func(string, string) agent.Result { t.Fatal("valid draft required a correction"); return agent.Result{} }
			_, problems, err = d.handIn(ctx, claim, result, noReask, func(agent.Message) error { return nil })
			must(t, err)
			if len(problems) > 0 {
				t.Fatal(problems)
			}
			select {
			case <-posted:
			case <-ctx.Done():
				t.Fatal("no hand-in received")
			}
			t.Logf("%s: preflight, sandboxed read-only lab turn, strict manual draft, preserved form and daemon hand-in passed (%d messages)", harness, len(messages))
		})
	}
}
