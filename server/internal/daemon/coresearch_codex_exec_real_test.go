package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
)

func TestRealCoresearchCodexExecAndResumeIgnoreHostileConfig(t *testing.T) {
	if os.Getenv("LYK_TEST_SKILLS_REAL") != "1" {
		t.Skip("opt-in installed CLI offline transport")
	}
	bin, err := exec.LookPath("codex")
	must(t, err)
	root := t.TempDir()
	run := filepath.Join(root, "parent", "chat")
	account := filepath.Join(cliAccountFixture(t), ".codex")
	must(t, os.MkdirAll(run, 0700))
	must(t, os.MkdirAll(account, 0700))
	t.Setenv("CODEX_HOME", account)

	marker := filepath.Join(run, "hostile-executed")
	must(t, os.WriteFile(filepath.Join(account, "AGENTS.md"), []byte("USER-CUSTOM-INSTRUCTIONS"), 0600))
	must(t, os.WriteFile(filepath.Join(account, "config.toml"), []byte("model=\"USER-HOSTILE\"\n[projects.\""+run+"\"]\ntrust_level=\"trusted\"\n"), 0600))
	for _, dir := range []string{run, filepath.Dir(run)} {
		must(t, os.MkdirAll(filepath.Join(dir, ".codex"), 0700))
		must(t, os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte("model=\"PROJECT-HOSTILE\"\n[mcp_servers.hostile]\ncommand=\"/usr/bin/touch\"\nargs=[\""+marker+"\"]\n"), 0600))
	}
	method := func(dir, name string) {
		t.Helper()
		must(t, os.MkdirAll(dir, 0700))
		must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Fixture method\n---\nCheck."), 0600))
	}
	method(filepath.Join(filepath.Dir(run), ".agents", "skills", "parent-method"), "parent-method")
	method(filepath.Join(account, "skills", "user-method"), "user-method")
	must(t, placeClaimedSkills("codex", run, []toolroute.RoutedSkill{{Name: "managed-method", Folder: skillFolder(t, map[string]string{"SKILL.md": "---\nname: managed-method\ndescription: Fixture managed method\n---\nCheck."})}}))
	requests := make(chan []byte, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			t.Error(err)
		}
		select {
		case requests <- b:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"offline fixture; no model invoked","type":"invalid_request_error","code":"fixture"}}`))
	}))
	defer upstream.Close()
	d := &daemon{cfg: Config{Home: filepath.Join(root, "daemon")}}
	var resume string
	for turn := 0; turn < 2; turn++ {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		opts := agent.ExecOptions{Dir: run, Coresearch: true, Guest: true, ConfigHome: account, Resume: resume, Env: []string{"COWRITE_FIXTURE_KEY=offline"}}
		opts.Sandbox = func(ctx context.Context, p agent.CLIPaths, name string, args ...string) (*exec.Cmd, error) {
			// This auth-free fixture may initialize a fresh account; production uses
			// the person's existing CLI state. All executable config masks still apply.

			extra := []string{"-c", `model_provider="offline_fixture"`, "-c", `model_providers.offline_fixture.name="Offline fixture"`, "-c", "model_providers.offline_fixture.base_url=" + string(offlineProviderJSON(upstream.URL)), "-c", `model_providers.offline_fixture.env_key="COWRITE_FIXTURE_KEY"`, "-c", `model_providers.offline_fixture.wire_api="responses"`, "-c", `model_providers.offline_fixture.requires_openai_auth=false`, "-c", `model_providers.offline_fixture.request_max_retries=0`, "-c", `model_providers.offline_fixture.stream_max_retries=0`}
			at := len(args) - 1
			if i := slices.Index(args, "resume"); i >= 0 {
				at = i
			}
			args = slices.Concat(args[:at], extra, args[at:])
			probe, e := d.cliSandbox(run, nil, false)(ctx, p, "/bin/sh", "-c", "echo changed > "+strconv.Quote(filepath.Join(account, "skills/.system/.codex-system-skills.marker")))
			if e != nil {
				return nil, e
			}
			if output, e := probe.CombinedOutput(); e == nil {
				t.Fatalf("agent shell can mutate builtin executable state: %s", output)
			}
			return d.cliSandbox(run, nil, false)(ctx, p, name, args...)
		}
		sess, err := (agent.Codex{Bin: bin}).Execute(ctx, "Check the managed method.", opts)
		must(t, err)
		var logs strings.Builder
		for m := range sess.Messages {
			logs.Write(m.Content)
		}
		result := <-sess.Done
		select {
		case raw := <-requests:
			var body map[string]any
			must(t, json.Unmarshal(raw, &body))
			text := string(raw)

			if strings.Contains(text, "PROJECT-HOSTILE") || strings.Contains(text, "USER-HOSTILE") || strings.Contains(text, "USER-CUSTOM-INSTRUCTIONS") || strings.Contains(text, "parent-method") || (!strings.Contains(text, "lyk-managed-method")) || strings.Contains(text, "user-method") {
				t.Fatalf("turn %d: wrong config/skills model=%v managed=%v user=%v parent=%v", turn, body["model"], strings.Contains(text, "lyk-managed-method"), strings.Contains(text, "user-method"), strings.Contains(text, "parent-method"))
			}

			t.Logf("turn %d: local offline request model=%v; only managed custom method; resume=%v", turn, body["model"], resume != "")
		default:
			t.Fatalf("turn %d never reached offline fixture: result=%+v logs=%s", turn, result, logs.String())
		}
		if result.SessionID == "" {
			t.Fatal("no persisted session for resume")
		}
		resume = result.SessionID
		cancel()
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("hostile project MCP/hook executed: %v", err)
		}
	}
}

func offlineProviderJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestRealCoresearchCodexIgnoresUninitializedOrStaleBundledSkills(t *testing.T) {
	if os.Getenv("LYK_TEST_SKILLS_REAL") != "1" {
		t.Skip("opt-in installed CLI offline transport")
	}
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale=%v", stale), func(t *testing.T) {
			bin, err := exec.LookPath("codex")
			must(t, err)
			account := filepath.Join(cliAccountFixture(t), ".codex")
			must(t, os.MkdirAll(account, 0700))
			t.Setenv("CODEX_HOME", account)
			if stale {
				must(t, os.MkdirAll(filepath.Join(account, "skills/.system"), 0700))
				must(t, os.WriteFile(filepath.Join(account, "skills/.system/.codex-system-skills.marker"), []byte("0000000000000000\n"), 0600))
			}
			run := t.TempDir()
			must(t, placeClaimedSkills("codex", run, nil))
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"message":"offline fixture","type":"invalid_request_error"}}`))
			}))
			defer upstream.Close()
			d := &daemon{cfg: Config{Home: t.TempDir()}}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			options := agent.ExecOptions{Dir: run, Coresearch: true, Guest: true, ConfigHome: account, Env: []string{"COWRITE_FIXTURE_KEY=offline"}}
			options.Sandbox = func(ctx context.Context, p agent.CLIPaths, name string, args ...string) (*exec.Cmd, error) {
				extra := []string{"-c", `model_provider="offline_fixture"`, "-c", `model_providers.offline_fixture.name="Offline"`, "-c", "model_providers.offline_fixture.base_url=" + string(offlineProviderJSON(upstream.URL)), "-c", `model_providers.offline_fixture.env_key="COWRITE_FIXTURE_KEY"`, "-c", `model_providers.offline_fixture.wire_api="responses"`, "-c", `model_providers.offline_fixture.requires_openai_auth=false`, "-c", `model_providers.offline_fixture.request_max_retries=0`, "-c", `model_providers.offline_fixture.stream_max_retries=0`}
				args = slices.Concat(args[:len(args)-1], extra, args[len(args)-1:])
				return d.cliSandbox(run, nil, false)(ctx, p, name, args...)
			}
			session, err := (agent.Codex{Bin: bin}).Execute(ctx, "No model should run.", options)
			if err == nil {
				for range session.Messages {
				}
				err = (<-session.Done).Err
			}
			if requests.Load() != 1 {
				t.Fatalf("managed turn blocked by unused builtin state: requests=%d err=%v", requests.Load(), err)
			}
			if err != nil {
				t.Logf("offline fixture result and exact CLI diagnostics: %v", err)
			}
			if stale {
				data, e := os.ReadFile(filepath.Join(account, "skills/.system/.codex-system-skills.marker"))
				must(t, e)
				if string(data) != "0000000000000000\n" {
					t.Fatal("agent changed bundled skill state")
				}
			}
		})
	}
}
