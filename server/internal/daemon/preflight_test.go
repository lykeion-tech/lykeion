package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/apiclient"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

// fakeCLI writes an executable under HOME, which the sandbox lets a CLI read.
func fakeCLI(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(os.Getenv("HOME"), "preflight-bin", name)
	must(t, os.MkdirAll(filepath.Dir(p), 0700))
	must(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0700))
	return p
}

const (
	claudeProbes = `case "$1" in --version) echo "2.1.0 (Claude Code)"; exit 0;; --help) echo "  --json-schema <schema>  JSON Schema"; exit 0;; esac
`
	// isolatedHello checks the hello runs as a study run does: in the
	// sandbox, with the door as its MCP server and no person's token.
	isolatedHello = `case "$LYK_TOKEN" in lyk_*) exit 8;; esac
[ -z "$LYK_DOOR_TOKEN" ] || exit 7
for arg do
  if [ "$prev" = --mcp-config ]; then grep -q lyk_door "$arg" || exit 6; found=1; break; fi
  prev="$arg"
done
[ "$found" = 1 ] || exit 6
touch "$HOME/escaped" 2>/dev/null && exit 9
IFS= read -r first
`
)

func preflightHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("LYK_TOKEN", "lyk_owner_must_never_leak")
}

// Each check fails with its own reason, in order: an earlier failure wins
// over every later one, and a healthy CLI passes.
func TestPreflightOrder(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	brokenSandbox := errors.New("sandbox-exec: refused")
	for _, tc := range []struct {
		name, harness, body string
		sandboxFails        bool
		doorFails           bool
		want                string
	}{
		{"missing", "claude", "", true, true, agent.ReasonCLIMissing},
		{"no version", "claude", "exit 1\n", true, true, agent.ReasonCLIMissing},
		{"too old", "claude", `case "$1" in --version) echo 1.0.0; exit 0;; --help) echo "  --print"; exit 0;; esac` + "\n", true, true, agent.ReasonCLITooOld},
		{"sandbox", "claude", claudeProbes, true, true, agent.ReasonSandbox},
		{"door", "claude", claudeProbes, false, true, agent.ReasonDoor},
		{"signed out", "claude", claudeProbes + "IFS= read -r first\necho 'Not logged in · Please run /login' >&2\nexit 1\n", false, false, agent.ReasonSignIn},
		{"oauth session expired", "claude", claudeProbes + "IFS= read -r first\necho 'Failed to authenticate: OAuth session expired and could not be refreshed' >&2\nexit 1\n", false, false, agent.ReasonSignIn},
		{"says no", "claude", claudeProbes + isolatedHello + fmtAnswer(`{"ok":false}`), false, false, agent.ReasonCLIBroken},
		{"provider unreachable", "claude", claudeProbes + "IFS= read -r first\necho 'API Error: Connection error (ECONNRESET)' >&2\nexit 1\n", false, false, agent.ReasonProviderNetwork},
		{"no answer", "claude", claudeProbes + "IFS= read -r first\nexit 0\n", false, false, agent.ReasonCLIBroken},
		// The hello is one turn even where Claude's help doesn't list the flag.
		{"healthy", "claude", claudeProbes + `case "$*" in *"--max-turns 1"*) ;; *) exit 4;; esac
` + isolatedHello + fmtAnswer(`{"ok":true}`), false, false, ""},
		{"codex too old", "codex", `case "$1 $2" in "--version ") echo codex-cli 0.1; exit 0;; "exec --help") echo "  --json"; exit 0;; esac` + "\n", true, true, agent.ReasonCLITooOld},
		{"codex healthy", "codex", `case "$1 $2" in "--version ") echo codex-cli 0.160.0; exit 0;; "exec --help") echo "  --output-schema <FILE>"; exit 0;; "debug models") echo '{"models":[{"slug":"gpt-5.6-sol","visibility":"list"}]}'; exit 0;; esac
case "$*" in *"--output-schema "*.lyk-output-schema.json*) ;; *) exit 5;; esac
[ "$(cat .lyk-output-schema.json)" = '` + preflightSchema + `' ] || exit 10
case "$LYK_TOKEN" in lyk_*) exit 8;; esac
[ -n "$LYK_DOOR_TOKEN" ] || exit 7
touch "$HOME/escaped" 2>/dev/null && exit 9
cat >/dev/null
echo '{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
`, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preflightHome(t)
			bin := filepath.Join(os.Getenv("HOME"), "nowhere", tc.harness)
			if tc.body != "" {
				bin = fakeCLI(t, tc.harness, tc.body)
			}
			check, door := checkSandbox, doorAnswers
			t.Cleanup(func() { checkSandbox, doorAnswers = check, door })
			if tc.sandboxFails {
				checkSandbox = func(context.Context) error { return brokenSandbox }
			}
			if tc.doorFails {
				doorAnswers = func(context.Context, *Door) error { return errors.New("no answer") }
			}
			d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir(), Harnesses: map[string]string{tc.harness: bin}, NewBackend: agent.New}}
			reason, detail := d.preflight(t.Context(), tc.harness)
			if reason != tc.want {
				t.Fatalf("reason %q (%s), want %q", reason, detail, tc.want)
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "escaped")); err == nil {
				t.Fatal("the hello wrote outside its sandbox")
			}
			if left, _ := filepath.Glob(filepath.Join(d.cfg.Home, "preflight-*")); len(left) != 0 {
				t.Fatalf("hello folders left behind: %v", left)
			}
		})
	}
}

func TestPreflightReportsScratchRetirementFailure(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	preflightHome(t)
	bin := fakeCLI(t, "claude", claudeProbes+isolatedHello+fmtAnswer(`{"ok":true}`))
	remove := removePreflightScratch
	t.Cleanup(func() { removePreflightScratch = remove })
	removePreflightScratch = func(_ *agent.ScratchReservation) error {
		return errors.New("sensitive simulated cleanup detail")
	}
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir(), Harnesses: map[string]string{"claude": bin}, NewBackend: agent.New}}
	reason, detail := d.preflight(t.Context(), "claude")
	if reason != agent.ReasonCLIBroken || detail != "preflight scratch cleanup failed" {
		t.Fatalf("reason %q detail %q; cleanup failure must be reported safely", reason, detail)
	}
	left, err := filepath.Glob(filepath.Join(d.cfg.Home, "preflight-*"))
	must(t, err)
	if len(left) != 1 {
		t.Fatalf("expected preserved scratch, got %v", left)
	}
	if _, err := os.Lstat(filepath.Join(left[0], ".mcp.json")); err != nil {
		t.Fatalf("guard not preserved: %v", err)
	}
	again, err := agent.ReserveScratch(left[0])
	must(t, err)
	defer again.Release()
	must(t, again.Remove())
}

func fmtAnswer(output string) string {
	return "echo '{\"type\":\"result\",\"is_error\":false,\"session_id\":\"s\",\"result\":\"\",\"structured_output\":" + output + ",\"modelUsage\":{\"m\":{\"inputTokens\":1,\"outputTokens\":1,\"cacheReadInputTokens\":0,\"cacheCreationInputTokens\":0,\"costUSD\":0}}}'\n"
}

// The real door answers the preflight's MCP check.
func TestPreflightDoorAnswers(t *testing.T) {
	door := &Door{Child: func() agent.ExecOptions { return agent.ExecOptions{} }}
	must(t, door.Start())
	defer door.Close()
	must(t, doorAnswers(t.Context(), door))
	door.Token = "wrong"
	if doorAnswers(t.Context(), door) == nil {
		t.Fatal("the door answered a wrong token")
	}
}

// Failed runs ask for at most one check per CLI per gap, counted from its
// last check of any kind; a fence breach and an agent's own failure ask for
// none.
func TestPreflightRecheckIsDeduped(t *testing.T) {
	gap := recheckGap
	t.Cleanup(func() { recheckGap = gap })
	recheckGap = 300 * time.Millisecond
	var calls atomic.Int32
	d := &daemon{rechecks: make(chan string, 4), cfg: Config{Harnesses: map[string]string{"claude": "claude"}, Preflight: func(context.Context, string) (string, string) { calls.Add(1); return "", "" }}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go d.preflights(ctx)
	// Right after the pass at start, a failure asks in vain.
	d.recheck("claude", agent.ReasonSignIn)
	d.recheck("claude", agent.ReasonFenceBreach)
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d checks right after the start pass", n)
	}
	time.Sleep(300 * time.Millisecond)
	d.recheck("claude", agent.ReasonFenceBreach)
	d.recheck("claude", agent.ReasonAgentError)
	d.recheck("claude", agent.ReasonRuntimeOffline)
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d checks for failures the machine didn't cause", n)
	}
	for range 3 {
		d.recheck("claude", agent.ReasonSignIn)
		time.Sleep(20 * time.Millisecond)
	}
	waitFor(t, func() bool { return calls.Load() >= 1 })
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d checks within the gap", n)
	}
}

func (f *daemonFixture) machine(t *testing.T) (reason *string, open int) {
	t.Helper()
	err := f.env.Owner.QueryRow(t.Context(), `SELECT unhealthy_reason, (SELECT count(*) FROM inbox_item i WHERE i.runtime_id = r.id AND i.kind = 'waiting_for_machine' AND i.resolved_at IS NULL)::int FROM runtime r WHERE r.workspace_id = $1`, f.ws).Scan(&reason, &open)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, -1 // not registered yet
	}
	must(t, err)
	return reason, open
}

// A failed preflight at start flags the machine before it claims anything; a
// passing one on the next start clears it, resolves the wait and lets the
// waiting study run.
func TestPreflightOnRestartClearsTheMachine(t *testing.T) {
	f := fixture(t)
	f.cfg.Preflight = func(context.Context, string) (string, string) { return agent.ReasonCLIBroken, "no answer" }
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { r, open := f.machine(t); return r != nil && *r == agent.ReasonCLIBroken && open == 1 })
	time.Sleep(200 * time.Millisecond)
	stopDaemon(t, cancel, done)
	if s := f.status(t); s != "queued" {
		t.Fatalf("a flagged machine took the run: %s", s)
	}

	// The real preflight, on a CLI that passes it; study runs go to the
	// fixture's own fake.
	f.cfg.Preflight = nil
	f.cfg.Harnesses["claude"] = fakeCLI(t, "claude", claudeProbes+`case "$*" in *--json-schema*) ;; *) exec `+f.cfg.Harnesses["claude"]+` "$@";; esac
`+isolatedHello+fmtAnswer(`{"ok":true}`))
	cancel, done = startDaemon(t, f.cfg)
	waitFor(t, func() bool { r, open := f.machine(t); return r == nil && open == 0 })
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
}

// A run the machine failed asks for a preflight at once, whose pass clears
// the machine.
func TestPreflightAfterInfraFailure(t *testing.T) {
	f := fixture(t)
	var calls atomic.Int32
	gap := recheckGap
	t.Cleanup(func() { recheckGap = gap })
	recheckGap = 0 // the run fails moments after the pass at start
	f.cfg.Preflight = func(context.Context, string) (string, string) { calls.Add(1); return "", "" }
	must(t, os.WriteFile(f.cfg.Harnesses["claude"], []byte("#!/bin/sh\nIFS= read -r first\necho 'Not logged in · Please run /login' >&2\nexit 1\n"), 0700))
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.parked(t) })
	waitFor(t, func() bool { return calls.Load() == 2 })
	waitFor(t, func() bool { r, open := f.machine(t); return r == nil && open == 0 })
	stopDaemon(t, cancel, done)
}

// The daemon's later passes leave a fence breach flagged; its pass at start
// clears it.
func TestPreflightStartPassAloneClearsAFenceBreach(t *testing.T) {
	f := fixture(t)
	d := &daemon{cfg: f.cfg, api: &apiclient.Client{Server: f.cfg.Server, Token: f.cfg.Token, HTTP: http.DefaultClient}, id: "fence-daemon", knownWorkspaces: map[uuid.UUID]bool{}}
	must(t, d.register(t.Context(), d.id))
	_, err := f.env.Owner.Exec(t.Context(), `UPDATE runtime SET unhealthy_reason = 'fence_breach' WHERE workspace_id = $1`, f.ws)
	must(t, err)
	d.checkMachine(t.Context(), "claude", false)
	if r, _ := f.machine(t); r == nil || *r != agent.ReasonFenceBreach {
		t.Fatalf("a later pass cleared a fence breach: %v", r)
	}
	d.checkMachine(t.Context(), "claude", true)
	if r, _ := f.machine(t); r != nil {
		t.Fatalf("the start pass left %v", *r)
	}
}

// A hello that runs out of time is a timeout, not a broken CLI.
func TestPreflightHelloTimeout(t *testing.T) {
	preflightHome(t)
	turn := preflightTurn
	t.Cleanup(func() { preflightTurn = turn })
	preflightTurn = 2 * time.Second
	bin := fakeCLI(t, "claude", claudeProbes+"IFS= read -r first\nsleep 30\n")
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir(), Harnesses: map[string]string{"claude": bin}, NewBackend: agent.New}}
	if reason, detail := d.preflight(t.Context(), "claude"); reason != "timeout" {
		t.Fatalf("reason %q (%s), want timeout", reason, detail)
	}
}

// A transient failure of the hello is tried again once before anything is
// reported; only a second failure the owner must fix flags the machine.
func TestPreflightRetriesATransientFailure(t *testing.T) {
	retry := transientRetry
	t.Cleanup(func() { transientRetry = retry })
	transientRetry = 0
	for _, tc := range []struct {
		name    string
		results []string
		want    string
	}{
		{"then passes", []string{agent.ReasonProviderNetwork, ""}, ""},
		{"twice transient", []string{"timeout", agent.ReasonContextExhausted}, ""},
		{"then broken", []string{agent.ReasonProviderNetwork, agent.ReasonSignIn}, agent.ReasonSignIn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture(t)
			var calls atomic.Int32
			f.cfg.Preflight = func(context.Context, string) (string, string) {
				n := calls.Add(1)
				return tc.results[min(int(n), len(tc.results))-1], "detail"
			}
			d := &daemon{cfg: f.cfg, api: &apiclient.Client{Server: f.cfg.Server, Token: f.cfg.Token, HTTP: http.DefaultClient}, id: "retry-daemon", knownWorkspaces: map[uuid.UUID]bool{}}
			must(t, d.register(t.Context(), d.id))
			d.checkMachine(t.Context(), "claude", false)
			if n := calls.Load(); n != 2 {
				t.Fatalf("%d checks, want 2", n)
			}
			r, _ := f.machine(t)
			if got := ""; r != nil {
				got = *r
				if got != tc.want {
					t.Fatalf("flagged %q, want %q", got, tc.want)
				}
			} else if tc.want != "" {
				t.Fatalf("not flagged, want %q", tc.want)
			}
		})
	}
}

// While a machine is flagged for a cause that clears by itself, its CLI is
// checked every few minutes rather than every half hour.
func TestPreflightRechecksSoonWhileASelfClearingCauseHolds(t *testing.T) {
	soon := softRecheckEvery
	t.Cleanup(func() { softRecheckEvery = soon })
	softRecheckEvery = 50 * time.Millisecond
	for _, tc := range []struct {
		reason string
		soon   bool
	}{{agent.ReasonDoor, true}, {agent.ReasonCLIBroken, true}, {agent.ReasonSignIn, false}} {
		t.Run(tc.reason, func(t *testing.T) {
			var calls atomic.Int32
			d := &daemon{rechecks: make(chan string, 4), cfg: Config{Harnesses: map[string]string{"claude": "claude"}, Preflight: func(context.Context, string) (string, string) {
				calls.Add(1)
				return tc.reason, ""
			}}}
			d.checkMachine(t.Context(), "claude", true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go d.preflights(ctx)
			time.Sleep(400 * time.Millisecond)
			if n := calls.Load(); tc.soon != (n >= 3) {
				t.Fatalf("%d checks in 400ms", n)
			}
		})
	}
}
