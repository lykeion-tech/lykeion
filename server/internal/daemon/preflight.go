package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

const (
	preflightPrompt = `Reply with {"ok": true}.`
	preflightSchema = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`
	// preflightEvery is how often an online machine checks its CLIs again.
	preflightEvery = 30 * time.Minute
)

var (
	// preflightTurn caps the hello turn.
	preflightTurn = 60 * time.Second
	// recheckGap is the least time between a CLI's last check and one a
	// failed run asks for.
	recheckGap = time.Minute
	// transientRetry is how long a hello that failed for a passing cause
	// waits before it is tried again.
	transientRetry = time.Minute
	// softRecheckEvery is how often a CLI is checked while its last check
	// failed for a cause that clears without the owner.
	softRecheckEvery = 2 * time.Minute
)

// schemaFlags is the help command each CLI lists its schema flag in, and
// the flag: a CLI without it can't hold a run's answer to a shape.
var schemaFlags = map[string]struct {
	help []string
	flag string
}{
	"claude": {[]string{"--help"}, "--json-schema"},
	"codex":  {[]string{"exec", "--help"}, "--output-schema"},
}

// checkSandbox and doorAnswers are the sandbox and door checks; tests
// replace them to fail.
var (
	checkSandbox = sandbox.Check
	doorAnswers  = func(ctx context.Context, door *Door) error {
		client := mcp.NewClient(&mcp.Implementation{Name: "lyk-preflight", Version: "0.1.0"}, nil)
		s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: door.URL, HTTPClient: &http.Client{Transport: bearer(door.Token)}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			return err
		}
		defer s.Close()
		tools, err := s.ListTools(ctx, nil)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(tools.Tools, func(t *mcp.Tool) bool { return t.Name == "call_tool" }) {
			return errors.New("the door offers no call_tool")
		}
		return nil
	}
)

// preflight checks one CLI the way a study run uses it: it starts and
// reports its version, knows the schema flag, the sandbox works, the door
// answers, and a one-turn hello inside the sandbox, with the door as its MCP
// server, returns {"ok": true}. An empty reason means healthy.
func (d *daemon) preflight(ctx context.Context, harness string) (reason string, detail string) {
	bin := d.cfg.Harnesses[harness]
	if bin == "" || agent.DetectVersion(ctx, bin) == "" {
		return agent.ReasonCLIMissing, harness + " did not report its version"
	}
	probe := schemaFlags[harness]
	help, _ := cliOutput(ctx, bin, probe.help...)
	if !strings.Contains(help, probe.flag) {
		return agent.ReasonCLITooOld, harness + " has no " + probe.flag
	}
	if err := checkSandbox(ctx); err != nil {
		return agent.ReasonSandbox, err.Error()
	}

	dir, err := os.MkdirTemp(d.cfg.Home, "preflight-")
	if err != nil {
		return agent.ReasonCLIBroken, err.Error()
	}
	reservation, err := agent.ReserveScratch(dir)
	if err != nil {
		os.RemoveAll(dir)
		return agent.ReasonCLIBroken, err.Error()
	}
	defer func() { reservation.Remove(); reservation.Release() }()
	path, err := runPath()
	if err != nil {
		return agent.ReasonCLIBroken, err.Error()
	}
	private, err := d.privatePaths()
	if err != nil {
		return agent.ReasonCLIBroken, err.Error()
	}
	// The same isolation as a study run's CLI: its folder, sandbox, door
	// and the adapter's own flags; no study, no run token.
	opts := agent.ExecOptions{ScratchReservation: reservation, Dir: dir, OutputSchema: preflightSchema, CacheDir: d.runCacheDir(""),
		Env: []string{"PATH=" + path, "CLAUDE_CODE_MAX_OUTPUT_TOKENS=2000"}}
	opts.Sandbox = d.cliSandbox(dir, nil, false)
	// One turn is all the hello needs. Claude accepts --max-turns even where
	// its help doesn't list it.
	if harness == "claude" {
		opts.MaxTurns = 1
	}
	door := &Door{Server: d.cfg.Server, Sources: d.sourcesURL(), Harness: harness, PrivatePaths: private, PrivateRoots: []string{d.cfg.Home}, SandboxErr: d.sandboxErr,
		Child: func() agent.ExecOptions { return opts }}
	if err := door.Start(); err != nil {
		return agent.ReasonDoor, err.Error()
	}
	defer door.Close()
	turn, cancel := context.WithTimeout(ctx, preflightTurn)
	defer cancel()
	if err := doorAnswers(turn, door); err != nil {
		return agent.ReasonDoor, err.Error()
	}
	opts.DoorURL, opts.DoorToken = door.URL, door.Token
	opts.Env = append(opts.Env, "LYK_DOOR="+door.URL, "LYK_DOOR_TOKEN="+door.Token)

	backend, err := d.cfg.NewBackend(harness, bin)
	if err != nil {
		return agent.ReasonCLIBroken, err.Error()
	}
	var res agent.Result
	sess, err := backend.Execute(turn, preflightPrompt, opts)
	if err == nil {
		var ok bool
		if res, ok = <-sess.Done; !ok {
			res.Err = errors.New("backend closed without a result")
		}
		err = res.Err
	}
	if err == nil && turn.Err() != nil {
		err = turn.Err()
	}
	if err != nil {
		reason := failReason(res.Reason, harness, err)
		if errors.Is(turn.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			reason = "timeout"
		}
		if reason == agent.ReasonAgentError {
			reason = agent.ReasonCLIBroken
		}
		return reason, err.Error()
	}
	var answer struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(res.Output), &answer) != nil || !answer.OK {
		return agent.ReasonCLIBroken, fmt.Sprintf("the hello turn answered %.200q", res.Output)
	}
	return "", ""
}

// cliOutput runs a CLI outside the sandbox, as its version probe does, and
// returns what it printed.
func cliOutput(ctx context.Context, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, agent.VersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// checkMachine runs one CLI's preflight and reports it for every runtime of
// that CLI: a pass clears the runtime and its waits, a failure flags it.
// Only the pass at start clears a fence breach.
func (d *daemon) checkMachine(ctx context.Context, harness string, start bool) {
	check := d.cfg.Preflight
	if check == nil {
		check = d.preflight
	}
	reason, detail := check(ctx, harness)
	if transient[reason] && ctx.Err() == nil {
		// A passing cause is tried once more before anything is reported.
		slog.Info("preflight failed for a passing cause; trying again", "harness", harness, "reason", reason, "detail", detail)
		select {
		case <-ctx.Done():
		case <-time.After(transientRetry):
			reason, detail = check(ctx, harness)
		}
	}
	if ctx.Err() != nil {
		return
	}
	d.mu.Lock()
	if d.selfClearing == nil {
		d.selfClearing = map[string]bool{}
	}
	d.selfClearing[harness] = selfClearing[reason]
	d.mu.Unlock()
	if transient[reason] {
		// Twice a passing cause: nothing the owner must fix, so the machine
		// is not flagged; it is checked again soon.
		slog.Warn("preflight failed twice for a passing cause; not flagged", "harness", harness, "reason", reason, "detail", detail)
		return
	}
	if reason != "" {
		slog.Warn("preflight failed", "harness", harness, "reason", reason, "detail", detail)
	}
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	for _, ref := range d.references() {
		d.mu.Lock()
		h := d.harnessOf[ref.RuntimeID]
		d.mu.Unlock()
		if h != harness {
			continue
		}
		body := map[string]any{"workspace_id": ref.WorkspaceID, "ok": reason == "", "start": start, "reason": reason, "detail": detail}
		if err := d.api.Do(ctx, "POST", "/api/daemon/runtimes/"+ref.RuntimeID.String()+"/health", body, nil); err != nil {
			slog.Warn("preflight report failed", "harness", harness, "err", err)
		}
	}
}

func (d *daemon) preflightAll(ctx context.Context, start bool) {
	for h := range d.cfg.Harnesses {
		d.checkMachine(ctx, h, start)
	}
}

// preflights checks every CLI every preflightEvery, or every
// softRecheckEvery while one failed its last check for a cause that clears
// by itself, and one CLI again when a run on it failed for the machine's
// sake, at most once per recheckGap.
func (d *daemon) preflights(ctx context.Context) {
	// Every CLI was just checked by the pass at start.
	checked := map[string]time.Time{}
	last := time.Now()
	all := func() {
		last = time.Now()
		for h := range d.cfg.Harnesses {
			checked[h] = last
		}
	}
	all()
	for {
		every := preflightEvery
		d.mu.Lock()
		for _, soon := range d.selfClearing {
			if soon {
				every = softRecheckEvery
			}
		}
		d.mu.Unlock()
		t := time.NewTimer(time.Until(last.Add(every)))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			all()
			d.preflightAll(ctx, false)
		case h := <-d.rechecks:
			t.Stop()
			if time.Since(checked[h]) >= recheckGap {
				checked[h] = time.Now()
				d.checkMachine(ctx, h, false)
			}
		}
	}
}

// transient are the hello's failures that pass by themselves: they are tried
// again and never flag a machine.
var transient = map[string]bool{agent.ReasonProviderNetwork: true, "timeout": true, agent.ReasonContextExhausted: true, agent.ReasonRuntimeOffline: true}

// selfClearing are the causes a machine may be flagged for that its owner
// needn't fix, so its CLI is checked again soon.
var selfClearing = map[string]bool{agent.ReasonRuntimeOffline: true, "timeout": true, agent.ReasonProviderNetwork: true, agent.ReasonDoor: true, agent.ReasonCLIBroken: true}

// recheck asks for a CLI's preflight after a run failed with reason. A fence
// breach asks for none: the hello can't see its cause, and only the owner or
// a restart clears it.
func (d *daemon) recheck(harness, reason string) {
	if harness == "" || !service.Infra(reason) || reason == agent.ReasonFenceBreach || reason == agent.ReasonRuntimeOffline {
		return
	}
	select {
	case d.rechecks <- harness:
	default:
	}
}
