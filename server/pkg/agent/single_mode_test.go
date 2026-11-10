package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func testSandbox(ctx context.Context, paths CLIPaths, bin string, args ...string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, bin, args...), nil
}
func TestExecuteRefusesWithoutSandbox(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	bin := script(t, "touch '"+marker+"'")
	for _, b := range []Backend{Claude{Bin: bin}, Codex{Bin: bin}} {
		if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()}); !errors.Is(err, ErrNoSandbox) {
			t.Fatalf("got %v", err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("process started: %v", err)
	}
	if _, err := OpenNamingSession(context.Background(), "claude", bin, t.TempDir(), "", nil); !errors.Is(err, ErrNoSandbox) {
		t.Fatal(err)
	}
}
func TestChildEnvCaches(t *testing.T) {
	cache := t.TempDir()
	env := "\n" + strings.Join(childEnv(ExecOptions{CacheDir: cache}, cache), "\n") + "\n"
	for k, v := range map[string]string{"HOME": os.Getenv("HOME"), "npm_config_cache": filepath.Join(cache, "npm"), "UV_CACHE_DIR": filepath.Join(cache, "uv"), "UV_PYTHON_INSTALL_DIR": filepath.Join(cache, "uv-python"), "UV_TOOL_DIR": filepath.Join(cache, "uv-tools")} {
		if !strings.Contains(env, "\n"+k+"="+v+"\n") {
			t.Errorf("missing %s=%s", k, v)
		}
	}
}

func collectWithin(t *testing.T, s *Session, timeout time.Duration) ([]Message, Result) {
	t.Helper()
	var messages []Message
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case m, ok := <-s.Messages:
			if !ok {
				select {
				case r := <-s.Done:
					return messages, r
				case <-timer.C:
					t.Fatal("agent completion timed out")
				}
			}
			messages = append(messages, m)
		case <-timer.C:
			t.Fatal("agent messages timed out")
		}
	}
}

func TestCLIStartsThroughSandbox(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			terminal := `{"type":"result","is_error":false}`
			if h == "codex" {
				terminal = `{"type":"turn.completed","usage":{}}`
			}
			bin := script(t, "echo '"+terminal+"'")
			wrapped := filepath.Join(t.TempDir(), "wrapped")
			wrapper := script(t, `echo wrapped > '`+wrapped+`'; exec "$@"`)
			called := false
			opts := ExecOptions{Dir: t.TempDir(), Model: "gpt-5", Sandbox: func(ctx context.Context, paths CLIPaths, name string, args ...string) (*exec.Cmd, error) {
				called = true
				if name != bin || len(paths.Write) == 0 {
					t.Fatalf("missing launch data: %s %+v", name, paths)
				}
				if h == "codex" && (len(paths.WriteFiles) == 0 || len(paths.WriteSQLite) == 0) {
					if runtime.GOOS == "darwin" {
						t.Fatalf("lost exact file grants: %+v", paths)
					}
				}
				return exec.CommandContext(ctx, wrapper, append([]string{name}, args...)...), nil
			}}
			b, _ := New(h, bin)
			session, err := b.Execute(context.Background(), "p", opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, r := collect(t, session); r.Err != nil {
				t.Fatal(r.Err)
			}
			if !called {
				t.Fatal("sandbox not called")
			}
			if _, err := os.Stat(wrapped); err != nil {
				t.Fatal(err)
			}
			original := exec.CommandContext(context.Background(), wrapper, bin)
			original.Dir = opts.Dir
			cmd, r, w, _, err := supervised(context.Background(), original)
			if err != nil {
				t.Fatal(err)
			}
			r.Close()
			w.Close()
			if cmd.Path != "/bin/sh" || !slices.Contains(cmd.Args, wrapper) {
				t.Fatalf("supervisor lost sandbox wrapper: %v", cmd.Args)
			}
		})
	}
}

func TestClaudeArgsSingleMode(t *testing.T) {
	for _, ro := range []bool{false, true} {
		args := filepath.Join(t.TempDir(), "args")
		bin := script(t, `printf '%s\n' "$@" > '`+args+`'; echo '{"type":"result","is_error":false}'`)
		session, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox, ReadOnly: ro})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, session)
		got, _ := os.ReadFile(args)
		text := string(got)
		if !strings.Contains(text, "--input-format\nstream-json\n") || !strings.Contains(text, "--strict-mcp-config") || strings.Contains(text, "--setting-sources") {
			t.Fatalf("args %s", text)
		}
		if ro && (!strings.Contains(text, "--permission-mode\ndontAsk\n") || !strings.Contains(text, "Edit(//**)\nBash\n")) {
			t.Fatalf("read-only args %s", text)
		}
	}
}

func TestCodexCatalogUsesSandbox(t *testing.T) {
	for _, catalog := range []string{testCatalog, "{\n \"models\": [{\"slug\": \"gpt-5.6-sol\", \"visibility\": \"list\"}]\n}"} {
		t.Run(catalog, func(t *testing.T) {
			var calls [][]string
			bin := catalogScript(t, catalog, testCatalog, filepath.Join(t.TempDir(), "args"))
			session, err := (Codex{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Sandbox: func(ctx context.Context, p CLIPaths, name string, args ...string) (*exec.Cmd, error) {
				if len(p.Write) == 0 {
					t.Fatal("catalog lost state grants")
				}
				calls = append(calls, append([]string(nil), args...))
				return testSandbox(ctx, p, name, args...)
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, r := collect(t, session); r.Err != nil {
				t.Fatal(r.Err)
			}
			if len(calls) != 2 || calls[0][0] != "debug" || calls[1][0] != "exec" {
				t.Fatalf("launches %v", calls)
			}
		})
	}
}

func TestSandboxErrorReleasesScratch(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		dir := t.TempDir()
		sentinel := errors.New("sandbox unavailable")
		b, _ := New(h, "does-not-exist")
		_, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir, Model: "gpt-5", Sandbox: func(context.Context, CLIPaths, string, ...string) (*exec.Cmd, error) { return nil, sentinel }})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		r, err := ReserveScratch(dir)
		if err != nil {
			t.Fatal(err)
		}
		r.Release()
	}
}

func TestRunFinishEmitterKeepsRecoverySpillOpen(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := command(ctx, script(t, "exit 0"), "", ExecOptions{Dir: dir, Sandbox: testSandbox})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	session, err := run(ctx, cmd, func(bool) {}, func([]byte, emitMessage, *Result) (bool, error) { return true, nil }, func(_ *Result, emit emitMessage) {
		for i := 0; i < 64; i++ {
			emit(Message{Type: "log", Content: raw(i)})
		}
		cancel()
		emit(Message{Type: "log", Content: raw("spilled before recovery")})
		emit(Message{Type: "text", Content: raw("recovered")})
		close(finished)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("finish blocked")
	}
	ms, res := collectWithin(t, session, 10*time.Second)
	if !errors.Is(res.Err, context.Canceled) || res.Reason != ReasonRuntimeOffline || len(ms) != 64 || len(res.RetainedEvidence) != 1 {
		t.Fatalf("%d %+v", len(ms), res)
	}
	b, err := os.ReadFile(res.RetainedEvidence[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var recovered Message
	if len(lines) != 2 || json.Unmarshal([]byte(lines[1]), &recovered) != nil || recovered.Type != "text" || string(recovered.Content) != `"recovered"` {
		t.Fatalf("spool lost final record: %s", b)
	}
}

func seedClaudeTranscript(t *testing.T, id string) string {
	t.Helper()
	dir := filepath.Join(accountHome("claude"), "projects", "synthetic-account-store")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return id
}

// A run with no tools keeps only Claude's structured answer, and Codex's
// shell and code mode are off in a read-only sandbox.
func TestNoToolsArgs(t *testing.T) {
	for _, c := range []struct {
		harness     string
		want, never []string
	}{
		{"claude", []string{"--tools\nStructuredOutput\n", "Edit(//**)\nBash\n", "--permission-mode\ndontAsk\n"}, []string{"--restricted"}},
		{"codex", []string{"-s\nread-only\n", "--disable\nshell_tool\n", "--disable\nunified_exec\n", "--disable\ncode_mode\n", "--strict-config\n", "--ignore-user-config\n"}, []string{"danger-full-access"}},
	} {
		t.Run(c.harness, func(t *testing.T) {
			args := filepath.Join(t.TempDir(), "args")
			b, _ := New(c.harness, script(t, `printf '%s\n' "$@" > '`+args+`'; echo '{"type":"result","is_error":false}'`))
			s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Model: "m", ReadOnly: true, NoTools: true})
			if err != nil {
				t.Fatal(err)
			}
			collect(t, s)
			got, _ := os.ReadFile(args)
			for _, w := range c.want {
				if !strings.Contains(string(got), w) {
					t.Errorf("no %q in\n%s", w, got)
				}
			}
			for _, n := range c.never {
				if strings.Contains(string(got), n) {
					t.Errorf("%q in\n%s", n, got)
				}
			}
		})
	}
}
