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
	"regexp"
	"slices"
	"strconv"
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
	removePreflightScratch = (*agent.ScratchReservation).Remove
	checkSandbox           = sandbox.Check
	doorAnswers            = func(ctx context.Context, door *Door) error {
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

func (d *daemon) refreshSandboxStatus(ctx context.Context) error {
	err := checkSandbox(ctx)
	d.sandboxMu.Lock()
	d.sandboxErr = err
	d.sandboxMu.Unlock()
	return err
}

func (d *daemon) sandboxStatus() error {
	d.sandboxMu.RLock()
	defer d.sandboxMu.RUnlock()
	return d.sandboxErr
}

// preflight checks one CLI the way a study run uses it: it starts and
// reports its version, knows the schema flag, the sandbox works, the door
// answers, and a one-turn hello inside the sandbox, with the door as its MCP
// server, returns {"ok": true}. An empty reason means healthy.
func (d *daemon) preflight(ctx context.Context, harness string) (reason string, detail string) {
	bin := d.cfg.Harnesses[harness]
	version := ""
	if bin != "" {
		version = agent.DetectVersion(ctx, bin)
	}
	if version == "" {
		return agent.ReasonCLIMissing, harness + " did not report its version"
	}
	if harness == "claude" && claudeTooOld(version) {
		return agent.ReasonCLITooOld, "claude " + version + " is older than 2.1.69"
	}
	probe := schemaFlags[harness]
	help, err := cliOutput(ctx, bin, probe.help...)
	if !strings.Contains(help, probe.flag) {
		// A help command that failed says nothing about the flag: report the
		// failure, which is checked again soon, not a CLI that is too old.
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "timeout", harness + " help did not answer in time"
		case err != nil:
			return agent.ReasonCLIBroken, harness + " help failed: " + err.Error()
		}
		return agent.ReasonCLITooOld, harness + " has no " + probe.flag
	}
	if err := d.refreshSandboxStatus(ctx); err != nil {
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
	defer func() {
		err := removePreflightScratch(reservation)
		reservation.Release()
		if err != nil {
			reason = agent.ReasonCLIBroken
			detail = "preflight scratch cleanup failed"
		}
	}()
	path, err := handInPath()
	if err != nil {
		return agent.ReasonCLIBroken, err.Error()
	}
	private, err := d.privatePaths()
	if err != nil {
		return agent.ReasonCLIBroken, err.Error()
	}
	// The same isolation as a hand-in run's CLI: its folder, sandbox, door,
	// PATH without lyk and the adapter's own flags; no study, no run token.
	opts := agent.ExecOptions{ScratchReservation: reservation, Dir: dir, OutputSchema: preflightSchema, CacheDir: d.runCacheDir(""),
		Env: []string{"PATH=" + path, "CLAUDE_CODE_MAX_OUTPUT_TOKENS=2000"}}
	opts.Sandbox = d.preflightSandbox(dir, reservation)
	// One turn is all the hello needs. Claude accepts --max-turns even where
	// its help doesn't list it.
	if harness == "claude" {
		opts.MaxTurns = 1
	}
	door := &Door{Server: d.cfg.Server, Sources: d.sourcesURL(), Harness: harness, PrivatePaths: private, PrivateRoots: []string{d.cfg.Home}, SandboxErr: d.sandboxStatus(),
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
	opts.Env = append(opts.Env, doorEnv(true, harness, door)...)

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
	if ctx.Err() != nil {
		err = ctx.Err() // killed at the deadline: say so, not "signal: killed"
	}
	return string(out), err
}

// checkMachine runs one CLI's preflight and reports it for every runtime of
// that CLI: a pass clears the runtime and its waits, a failure flags it.
// Only the pass at start clears a fence breach.
func (d *daemon) checkMachine(ctx context.Context, harness string, start bool) {
	checkedAt := time.Now()
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
	if reason == agent.ReasonProviderQuota {
		d.pauseQuota(harness)
	}
	d.mu.Lock()
	newerQuota := d.quotaPaused[harness].After(checkedAt)
	d.mu.Unlock()
	if reason == "" && newerQuota {
		return
	}
	if err := d.reportHealth(ctx, harness, reason, detail, start); err != nil {
		slog.Warn("preflight report failed", "harness", harness, "err", err)
		return
	}
	if reason == "" {
		d.mu.Lock()
		newerQuota = d.quotaPaused[harness].After(checkedAt)
		if !newerQuota {
			delete(d.quotaPaused, harness)
		}
		d.mu.Unlock()
		if newerQuota {
			if err := d.reportHealth(ctx, harness, agent.ReasonProviderQuota, "Provider usage limit reached during preflight", false); err != nil {
				slog.Warn("quota report failed", "harness", harness, "err", err)
			}
		}
	}
}

func (d *daemon) reportHealth(ctx context.Context, harness, reason, detail string, start bool) error {
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	var reports error
	for _, ref := range d.references() {
		d.mu.Lock()
		h := d.harnessOf[ref.RuntimeID]
		d.mu.Unlock()
		if h != harness {
			continue
		}
		body := map[string]any{"workspace_id": ref.WorkspaceID, "ok": reason == "", "start": start, "reason": reason, "detail": detail}
		reports = errors.Join(reports, d.api.Do(ctx, "POST", "/api/daemon/runtimes/"+ref.RuntimeID.String()+"/health", body, nil))
	}
	return reports
}

func (d *daemon) pauseQuota(harness string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.quotaPaused == nil {
		d.quotaPaused = map[string]time.Time{}
	}
	d.quotaPaused[harness] = time.Now()
}

func (d *daemon) pauseQuotaReport(r Report) {
	if r.Kind != "fail" || r.FailureReason != agent.ReasonProviderQuota {
		return
	}
	d.mu.Lock()
	harness := d.harnessOf[r.RuntimeID]
	d.mu.Unlock()
	if harness != "" {
		d.pauseQuota(harness)
	}
}

func (d *daemon) claimableReferences(refs []runtimeRef) []runtimeRef {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]runtimeRef, 0, len(refs))
	for _, ref := range refs {
		if d.quotaPaused[d.harnessOf[ref.RuntimeID]].IsZero() {
			out = append(out, ref)
		}
	}
	return out
}

func (d *daemon) preflightAll(ctx context.Context, start bool) {
	for h := range d.cfg.Harnesses {
		d.checkMachine(ctx, h, start)
	}
}

// preflightDue keeps each CLI's schedule independent, so another CLI's soft
// rechecks cannot repeatedly probe an exhausted provider account.
func (d *daemon) preflightDue(harness string, checked time.Time) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	every := preflightEvery
	if paused := d.quotaPaused[harness]; !paused.IsZero() {
		if paused.After(checked) {
			checked = paused
		}
	} else if d.selfClearing[harness] {
		every = softRecheckEvery
	}
	return checked.Add(every)
}

func (d *daemon) preflights(ctx context.Context) {
	checked := map[string]time.Time{}
	for h := range d.cfg.Harnesses {
		checked[h] = time.Now()
	}
	for {
		next := time.Now().Add(preflightEvery)
		for h, last := range checked {
			if due := d.preflightDue(h, last); due.Before(next) {
				next = due
			}
		}
		t := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			for h, last := range checked {
				if !d.preflightDue(h, last).After(time.Now()) {
					d.checkMachine(ctx, h, false)
					checked[h] = time.Now()
				}
			}
		case h := <-d.rechecks:
			t.Stop()
			d.mu.Lock()
			paused := !d.quotaPaused[h].IsZero()
			d.mu.Unlock()
			if !paused && time.Since(checked[h]) >= recheckGap {
				d.checkMachine(ctx, h, false)
				checked[h] = time.Now()
			}
		}
	}
}

// transient are the hello's failures that pass by themselves: they are tried
// again and never flag a machine.
var transient = map[string]bool{agent.ReasonProviderNetwork: true, "timeout": true, agent.ReasonContextExhausted: true, agent.ReasonRuntimeOffline: true}

// selfClearing are the causes a machine may be flagged for that its owner
// needn't fix, so its CLI is checked again soon.
var selfClearing = map[string]bool{agent.ReasonRuntimeOffline: true, "timeout": true, agent.ReasonProviderNetwork: true, agent.ReasonDoor: true, agent.ReasonCLIBroken: true, agent.ReasonSandbox: true}

// recheck asks for a CLI's preflight after a run failed with reason. A fence
// breach asks for none: the hello can't see its cause, and only the owner or
// a restart clears it.
func (d *daemon) recheck(harness, reason string) {
	if harness == "" || !service.Infra(reason) || reason == agent.ReasonProviderQuota || reason == agent.ReasonFenceBreach || reason == agent.ReasonRuntimeOffline {
		return
	}
	select {
	case d.rechecks <- harness:
	default:
	}
}

// claudeVersion is the version number in Claude Code's version line.
var claudeVersion = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// claudeTooOld says whether a Claude Code version line names a release
// before 2.1.69, the first with --append-system-prompt-file, which hands a
// run its role. A line with no version number is let through.
func claudeTooOld(version string) bool {
	m := claudeVersion.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return slices.Compare(v[:], []int{2, 1, 69}) < 0
}
