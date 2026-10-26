package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"lykeion/server/internal/apiclient"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/pkg/agent"
)

func TestAnUnknownOwnerFailsClosed(t *testing.T) {
	state := t.TempDir()
	called := false
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: state, NewBackend: func(string, string) (agent.Backend, error) {
		called = true
		return nil, errors.New("backend was reached")
	}}}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
	c := claimed{}
	c.AgentRun.ID, c.AgentRun.WorkspaceID, c.AgentRun.ResponsibleUserID = uuid.New(), uuid.New(), uuid.New()
	runtimeID := uuid.New()
	c.AgentRun.RuntimeID = &runtimeID
	c.AgentRun.Attempt, c.AgentRun.Harness = 1, "codex"
	must(t, d.runAgentRun(t.Context(), c))
	r, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: 1}))
	must(t, err)
	if called || r.Kind != "fail" || r.FailureReason != agent.ReasonAgentError || r.Error != "unknown runtime owner" {
		t.Fatalf("unknown owner did not fail closed before backend: called=%t report=%+v", called, r)
	}
}

func TestGuestRuntimeOwnerIsRecordedOnRegistrationAndRefresh(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(fmt.Sprint(refresh), func(t *testing.T) {
			ws, rt, owner := uuid.New(), uuid.New(), uuid.New()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" {
					json.NewEncoder(w).Encode([]map[string]any{{"id": ws}})
					return
				}
				json.NewEncoder(w).Encode([]map[string]any{{"id": rt, "owner_id": owner}})
			}))
			defer srv.Close()
			d := &daemon{cfg: Config{Harnesses: map[string]string{"codex": "codex"}}, id: "machine", api: &apiclient.Client{Server: srv.URL}, knownWorkspaces: map[uuid.UUID]bool{}}
			if refresh {
				must(t, d.refreshReferences(t.Context()))
			} else {
				must(t, d.register(t.Context(), d.id))
			}
			if d.ownerOf[rt] != owner {
				t.Fatal("registration lost the runtime owner")
			}
		})
	}
}

func TestAGuestRunGetsItsOwnHomeAndCache(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS guest runs fail closed until process isolation is enforced")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	ownerConfig := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", ownerConfig)
	must(t, os.MkdirAll(ownerConfig, 0700))
	must(t, os.WriteFile(filepath.Join(ownerConfig, "auth.json"), []byte("sign-in-fixture"), 0600))
	must(t, os.WriteFile(filepath.Join(ownerConfig, "config.toml"), []byte("model = 'owner-model'"), 0600))
	must(t, os.MkdirAll(filepath.Join(ownerConfig, "sessions"), 0700))
	must(t, os.WriteFile(filepath.Join(ownerConfig, "sessions", "rollout-owner-session.jsonl"), []byte("{}"), 0600))
	bin := filepath.Join(home, "installation", "codex")
	must(t, os.MkdirAll(filepath.Dir(bin), 0700))
	must(t, os.WriteFile(bin, []byte(`#!/bin/sh
cat >/dev/null
printf '{"type":"item.completed","item":{"type":"agent_message","text":"%s|%s"}}\n' "$CODEX_HOME" "$npm_config_cache"
echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0700))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	state := filepath.Join(home, ".lyk", "daemon")
	runRoot := filepath.Join(home, ".lyk", "runs")
	must(t, os.MkdirAll(runRoot, 0700))
	rt, owner, guest := uuid.New(), uuid.New(), uuid.New()
	d := &daemon{cfg: Config{Home: runRoot, StateDir: state, Server: srv.URL, Harnesses: map[string]string{"codex": bin}}, api: &apiclient.Client{Server: srv.URL}, ownerOf: map[uuid.UUID]uuid.UUID{rt: owner}}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
	d.messages = &Outbox{Dir: filepath.Join(state, "messages")}
	var output string
	var configured string
	d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(ctx context.Context, prompt string, o agent.ExecOptions) (*agent.Session, error) {
			if !o.Guest {
				t.Error("verified guest lost explicit environment isolation")
			}
			configured = o.ConfigHome
			if o.Resume != "" {
				t.Error("guest resumed owner's session")
			}
			sink := o.OnMessage
			o.OnMessage = func(m agent.Message) error {
				if m.Type == "text" {
					json.Unmarshal(m.Content, &output)
				}
				return sink(m)
			}
			return (agent.Codex{Bin: bin}).Execute(ctx, prompt, o)
		}), nil
	}
	c := claimed{Token: "ltk_guest"}
	c.AgentRun.ID, c.AgentRun.WorkspaceID, c.AgentRun.ResponsibleUserID = uuid.New(), uuid.New(), guest
	c.AgentRun.RuntimeID = &rt
	c.AgentRun.Harness, c.AgentRun.RequestedModel, c.AgentRun.SessionID = "codex", "fixture", "owner-session"
	c.AgentRun.Attempt = 1
	c.AgentRun.ContinuityScope = boardScope(uuid.New(), "analysis")
	d.scopes = map[string]bool{}
	must(t, d.finishContinuity(begin(t, d, c), agent.Result{SessionID: "owner-session"}))
	must(t, d.runAgentRun(t.Context(), c))
	report, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: 1}))
	must(t, err)
	expectedHome := filepath.Join(state, "guests", guest.String(), "codex")
	expectedCache := filepath.Join(d.cacheDir(), "guests", guest.String(), "npm")
	if report.Kind != "complete" || configured != expectedHome || output != expectedHome+"|"+expectedCache {
		t.Fatalf("guest launch: kind=%s error=%s home=%s output=%s", report.Kind, report.Error, configured, output)
	}
	if data, err := os.ReadFile(filepath.Join(expectedHome, "config.toml")); (err != nil && !os.IsNotExist(err)) || len(data) > 0 {
		t.Fatal("owner config was injected")
	}
}

func TestGuestDoorDoesNotReceiveCLIAuth(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "ambient-fixture")
	t.Setenv("CUSTOM_PERSONAL_VALUE", "ambient-custom-fixture")
	opts := agent.ExecOptions{Env: []string{"CLAUDE_CODE_OAUTH_TOKEN=guest-fixture", "KEEP=value"}}
	child := guestDoorOptions(opts)
	if slices.Contains(child.Env, "CLAUDE_CODE_OAUTH_TOKEN=guest-fixture") {
		t.Fatal("Door options retain CLI credential")
	}
	for _, env := range [][]string{agent.ChildEnv(child), connectorEnv(child, nil)} {
		for _, entry := range env {
			if strings.Contains(entry, "guest-fixture") || strings.Contains(entry, "ambient-fixture") || strings.Contains(entry, "ambient-custom-fixture") {
				t.Fatal("CLI credential passed downstream")
			}
		}
	}
	if !slices.Contains(opts.Env, "CLAUDE_CODE_OAUTH_TOKEN=guest-fixture") {
		t.Fatal("sanitizing door changed CLI environment")
	}
}

func TestMacGuestFailsClosedBeforeBackend(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS process-environment isolation guard")
	}
	state := t.TempDir()
	called := false
	rt, owner := uuid.New(), uuid.New()
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: state, NewBackend: func(string, string) (agent.Backend, error) { called = true; return nil, errors.New("backend reached") }}, ownerOf: map[uuid.UUID]uuid.UUID{rt: owner}}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
	c := claimed{}
	c.AgentRun.ID, c.AgentRun.WorkspaceID, c.AgentRun.ResponsibleUserID = uuid.New(), uuid.New(), uuid.New()
	c.AgentRun.RuntimeID = &rt
	c.AgentRun.Harness = "claude"
	c.AgentRun.Attempt = 1
	must(t, d.runAgentRun(t.Context(), c))
	report, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: 1}))
	must(t, err)
	if called || report.Kind != "fail" || report.FailureReason != agent.ReasonAgentError || report.Error != "shared runs are unavailable on macOS: process environment isolation is not enforced" {
		t.Fatalf("unsafe macOS guest launch: called=%t report=%+v", called, report)
	}
	if _, err := os.Stat(filepath.Join(state, "guests")); !os.IsNotExist(err) {
		t.Fatal("guest secrets prepared before platform guard")
	}
}

func TestGuestExistingRuntimeOwnerRefresh(t *testing.T) {
	ws, rt, owner := uuid.New(), uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/runtimes" {
			json.NewEncoder(w).Encode([]map[string]any{{"id": rt, "owner_id": owner, "daemon_id": "machine", "harness": "codex"}})
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{{"id": ws}})
	}))
	defer srv.Close()
	d := &daemon{cfg: Config{Harnesses: map[string]string{"codex": "codex"}}, id: "machine", api: &apiclient.Client{Server: srv.URL}, knownWorkspaces: map[uuid.UUID]bool{ws: true}, ownerOf: map[uuid.UUID]uuid.UUID{rt: uuid.New()}}
	must(t, d.refreshReferences(t.Context()))
	if d.ownerOf[rt] != owner {
		t.Fatal("refresh kept stale runtime owner")
	}
}

func TestGuestIsolationCapabilityRequest(t *testing.T) {
	previous := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, status := range []int{200, 400} {
		logs.Reset()
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var body map[string]any
			must(t, json.NewDecoder(r.Body).Decode(&body))
			want := ""
			if runtime.GOOS == "linux" {
				want = agent.GuestIsolationProtocol
			}
			got, _ := body["guest_isolation"].(string)
			if got != want {
				t.Errorf("guest protocol=%q want=%q", got, want)
			}
			if status == 400 {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"unsupported protocol"}`)
			} else {
				fmt.Fprint(w, `[]`)
			}
		}))
		d := &daemon{cfg: Config{Slots: 1}, api: &apiclient.Client{Server: srv.URL}, refs: []runtimeRef{{uuid.New(), uuid.New()}}}
		d.claim(t.Context())
		srv.Close()
		if status == 400 && (!strings.Contains(logs.String(), "claim failed") || !strings.Contains(logs.String(), "unsupported protocol")) {
			t.Fatalf("missing expected protocol refusal warning: %s", logs.String())
		}
		if calls != 1 {
			t.Fatalf("claim retried without guest isolation: %d", calls)
		}
	}
}
