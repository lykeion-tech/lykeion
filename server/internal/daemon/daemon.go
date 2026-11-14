// Package daemon registers runtimes and executes queued agent runs in free slots.
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
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
)

type Config struct {
	Server, Token, StateDir, Home, Device string
	Profile, HealthPath                   string
	Harnesses                             map[string]string
	Slots                                 int
	Poll, Heartbeat, Flush                time.Duration
	NewBackend                            func(harness, bin string) (agent.Backend, error)
	Ready                                 func() error
	OnExit                                func()
	// EnvDir holds the language bases and the studies' add-on folders; empty
	// gives runs no study environment.
	EnvDir string
	// Bases, when non-nil, replaces the bases the daemon builds at EnvDir.
	Bases *envs.Bases
	// Preflight, when non-nil, replaces the daemon's check of a CLI; an
	// empty reason means healthy.
	Preflight func(ctx context.Context, harness string) (reason, detail string)
}

func (c *Config) defaults() {
	if c.Slots == 0 {
		c.Slots = 2
	}
	if c.Poll == 0 {
		c.Poll = 30 * time.Second
	}
	if c.Heartbeat == 0 {
		c.Heartbeat = 15 * time.Second
	}
	if c.Flush == 0 {
		c.Flush = 10 * time.Second
	}
	if c.NewBackend == nil {
		c.NewBackend = agent.New
	}
	if c.Bases == nil && c.EnvDir != "" {
		c.Bases = envs.NewBases(c.EnvDir)
	}
}
func Detect() map[string]string {
	out := map[string]string{}
	for h, bin := range agent.Harnesses {
		if p, err := exec.LookPath(bin); err == nil {
			out[h] = p
		}
	}
	return out
}

type runtimeRef struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	RuntimeID   uuid.UUID `json:"runtime_id"`
}
type claimed struct {
	HasCommentSnapshot bool                       `json:"has_comment_snapshot,omitempty"`
	ReservedCommentIDs []uuid.UUID                `json:"reserved_comment_ids,omitempty"`
	Coresearch         *service.ClaimedCoresearch `json:"coresearch,omitempty"`
	// Rerun is what a rerun replays; set on a run of kind "rerun" only.
	Rerun         *service.ClaimedRerun `json:"rerun,omitempty"`
	PythonVersion string                `json:"python_version"`
	AgentRun      struct {
		ID                uuid.UUID  `json:"id"`
		WorkspaceID       uuid.UUID  `json:"workspace_id"`
		ResponsibleUserID uuid.UUID  `json:"responsible_user_id"`
		RuntimeID         *uuid.UUID `json:"runtime_id"`
		Attempt           int32      `json:"attempt"`
		Harness           string     `json:"harness"`
		Prompt            string     `json:"prompt"`
		Skill             string     `json:"skill"`
		SessionID         string     `json:"session_id"`
		// ContinuityScope keys session continuity; empty never resumes.
		ContinuityScope string `json:"continuity_scope"`
		// RequestedModel is the agent's model; empty runs the CLI's default.
		RequestedModel string `json:"requested_model"`
		// CardID is the run's study; nil for a run on no card.
		CardID      *uuid.UUID `json:"card_id"`
		BoardColumn string     `json:"board_column"`
		// Kind selects the study, Autoresearch, Coresearch, Aristotle, or rerun execution path.
		Kind string `json:"kind"`
	} `json:"agent_run"`
	Token string `json:"token"`
	// CallToken records the run's calls; only the door holds it.
	CallToken string            `json:"call_token"`
	MCPURL    string            `json:"mcp_url"`
	MCP       []agent.MCPServer `json:"mcp"`
	// Connectors are what the run may call through its tool door.
	Connectors          []pack.Connector                  `json:"connectors"`
	WorkspaceConnectors []service.WorkspaceConnectorClaim `json:"workspace_connectors,omitempty"`
	// Attachments are the study's files, placed read-only in the run folder.
	Attachments []service.ClaimedAttachment `json:"attachments"`
	Services    []service.ClaimedService    `json:"services"`
	// Skills are a Team member's own skills, placed where its CLI reads them.
	Skills []toolroute.RoutedSkill `json:"skills"`
	// OutputSchema is the schema the run's final answer, its hand-in, must
	// fit; empty for a run that owes none. Lead marks the run that works its
	// column through.
	OutputSchema string `json:"output_schema"`
	Lead         bool   `json:"lead"`
	// Leader marks a Team leader's lead run, whose door offers ask_member
	// and member_result.
	Leader bool `json:"leader"`
}
type daemon struct {
	modelProbeMu       sync.Mutex
	models             map[string][]agent.Model
	chatsMu            sync.Mutex
	chats              map[uuid.UUID]*chatHost
	coreActive         map[string]bool
	coreWork           map[uuid.UUID]bool
	attemptMu          sync.Mutex
	coreReceiptSync    func(*os.File) error
	coreReceiptMu      sync.Mutex
	coreReceiptFlights map[string]*coreReceiptFlight

	serviceCommand    func(sandbox.Policy, context.Context, string, ...string) (*exec.Cmd, error)
	serviceReadyLimit time.Duration
	serviceStopLimit  time.Duration
	stopControl       context.CancelFunc
	namingSlots       chan struct{}
	cfg               Config
	versions          map[string]string // harness → its CLI version, read once at start
	platform          agent.Platform    // the machine's platform, read once at start
	api               *apiclient.Client
	refs              []runtimeRef
	ownerOf           map[uuid.UUID]uuid.UUID
	harnessOf         map[uuid.UUID]string // runtime → its CLI
	rechecks          chan string          // CLIs a failed run asks to check again
	selfClearing      map[string]bool      // CLIs whose last check failed for a cause that clears by itself
	out, messages     *Outbox
	wake              chan struct{}
	failures          chan error
	mu                sync.Mutex
	active            map[string]bool
	wg                sync.WaitGroup
	rotation          int
	id                string
	knownWorkspaces   map[uuid.UUID]bool
	workspaceCount    int
	activeRefs        map[string]runtimeRef
	activeCancels     map[string]context.CancelFunc
	scopes            map[string]bool
	scratches         map[string]bool
	// bases are the machine's language bases; nil when the daemon has none.
	bases *envs.Bases
	// sandboxErr is why the sandbox failed its check at start; analysis code
	// runs nowhere while it is set.
	sandboxErr error
	// setUpConda rebuilds a rerun's conda environment; nil means
	// (*envs.Study).SetUpConda.
	setUpConda func(*envs.Study, context.Context, string, []string, string) (string, error)
	// rerunDoor, when set, adjusts a rerun's door before it starts.
	rerunDoor func(*Door)
	// rebuildLimit is how long a rerun may take to rebuild one language's
	// environment; 0 means envs.SetupLimit.
	rebuildLimit time.Duration
}

func daemonID(dir, server string) (string, error) {
	var state struct {
		DaemonID string `json:"daemon_id"`
		Server   string `json:"server"`
	}
	p := filepath.Join(dir, "daemon.json")
	b, err := os.ReadFile(p)
	if err == nil {
		if err = json.Unmarshal(b, &state); err != nil {
			return "", err
		}
		if state.DaemonID == "" || state.Server != strings.TrimRight(server, "/") {
			return "", errors.New("daemon state identity does not match server")
		}
		return state.DaemonID, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	state.DaemonID = uuid.NewString()
	state.Server = strings.TrimRight(server, "/")
	return state.DaemonID, atomicJSON(dir, "daemon.json", state)
}
func Run(ctx context.Context, cfg Config) error {
	cfg.defaults()
	if !strings.HasPrefix(cfg.Token, "lyk_") {
		return errors.New("daemon requires a personal lyk_ token")
	}
	if cfg.StateDir == "" || cfg.Home == "" || cfg.Slots < 1 || cfg.Slots > 64 || cfg.Poll <= 0 || cfg.Heartbeat <= 0 || cfg.Flush <= 0 {
		return errors.New("invalid daemon directories, slots or intervals")
	}
	if len(cfg.Harnesses) == 0 {
		return errors.New("no agent CLI found: install claude or codex")
	}
	lock, err := ownState(cfg.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	if cfg.OnExit != nil {
		defer cfg.OnExit()
	}
	if err = privateDir(cfg.Home); err != nil {
		return err
	}
	id, err := daemonID(cfg.StateDir, cfg.Server)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := &daemon{cfg: cfg, stopControl: cancel, api: &apiclient.Client{Server: cfg.Server, Token: cfg.Token, HTTP: &http.Client{Timeout: 30 * time.Second}}, wake: make(chan struct{}, 1), rechecks: make(chan string, 4), failures: make(chan error, 1), active: map[string]bool{}, id: id, knownWorkspaces: map[uuid.UUID]bool{}, activeRefs: map[string]runtimeRef{}, activeCancels: map[string]context.CancelFunc{}, scopes: map[string]bool{}, bases: cfg.Bases}
	d.messages = &Outbox{Dir: filepath.Join(cfg.StateDir, "messages"), Send: d.sendMessages}
	d.out = &Outbox{Dir: filepath.Join(cfg.StateDir, "outbox"), Send: d.send}
	if err = d.recoverCoresearch(); err != nil {
		return err
	}
	if err = d.recoverAttempts(ctx); err != nil {
		return err
	}
	d.versions = map[string]string{}
	for h, bin := range cfg.Harnesses {
		d.versions[h] = agent.DetectVersion(ctx, bin)
	}
	d.platform = agent.DetectPlatform(ctx)
	if err = d.register(ctx, id); err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil
		}
		return err
	}
	// Start before the initial generic outbox flush as well: old message
	// delivery must not delay terminal receipts or their lease renewals.
	receiptsDone := make(chan struct{})
	go func() {
		defer close(receiptsDone)
		d.every(ctx, min(cfg.Flush, 10*time.Second), func(ctx context.Context) { d.fatal(d.flushCoresearch(ctx)) })
	}()
	defer func() { cancel(); <-receiptsDone }()
	if err = d.flush(ctx); err != nil {
		return err
	}
	if cfg.HealthPath != "" {
		closeHealth, err := d.startHealth()
		if err != nil {
			return fmt.Errorf("start daemon health: %w", err)
		}
		defer closeHealth()
	}
	if cfg.Ready != nil {
		if err = cfg.Ready(); err != nil {
			return fmt.Errorf("announce daemon readiness: %w", err)
		}
	}
	if d.sandboxErr = sandbox.Check(ctx); d.sandboxErr != nil {
		slog.Warn("agents can't run on this machine: the sandbox isn't available", "err", d.sandboxErr)
	}
	background := func(fn func()) { d.wg.Add(1); go func() { defer d.wg.Done(); fn() }() }
	background(func() { d.every(ctx, cfg.Heartbeat, d.heartbeat) })
	background(func() { d.probeModels(ctx); d.every(ctx, time.Hour, d.probeModels) })
	background(func() { d.every(ctx, cfg.Flush, func(ctx context.Context) { d.fatal(d.flush(ctx)) }) })
	background(func() { d.every(ctx, time.Hour, func(ctx context.Context) { d.fatal(d.gc(ctx)) }) })
	background(func() { d.hints(ctx) })
	background(func() { d.every(ctx, min(time.Second, chatIdle/2), d.reapChats) })
	background(func() { d.every(ctx, 5*time.Second, d.sampleKernels) })
	background(func() { d.storageLoop(ctx) })
	background(func() { d.runtimeSkills(ctx); d.every(ctx, cfg.Poll, d.runtimeSkills) })
	// Before the first claim: a machine fixed while the daemon was down is
	// cleared, and one that broke is flagged. A restart is also how an owner
	// who fixed a fence breach clears it.
	preflightStart := time.Now()
	d.preflightAll(ctx, true)
	slog.Info("start preflight done", "took", time.Since(preflightStart).Round(time.Millisecond))
	background(func() { d.preflights(ctx) })
	ticker := time.NewTicker(cfg.Poll)
	defer ticker.Stop()
	var runErr error
loop:
	for {
		if ctx.Err() != nil {
			break
		}
		d.claim(ctx)
		select {
		case <-ctx.Done():
			break loop
		case runErr = <-d.failures:
			break loop
		case <-ticker.C:
		case <-d.wake:
		}
	}
	cancel()
	d.wg.Wait()
	<-receiptsDone
	runErr = errors.Join(runErr, d.stopChats())
	// A concurrent child storage failure must not disappear during shutdown.
	select {
	case err := <-d.failures:
		runErr = errors.Join(runErr, err)
	default:
	}
	return runErr
}

// setHarness records a runtime's CLI; the caller holds d.mu.
func (d *daemon) setHarness(runtime uuid.UUID, harness string) {
	if d.harnessOf == nil {
		d.harnessOf = map[uuid.UUID]string{}
	}
	d.harnessOf[runtime] = harness
}
func (d *daemon) fatal(err error) {
	if err != nil {
		select {
		case d.failures <- err:
		default:
		}
	}
}
func (d *daemon) signal() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
func (d *daemon) every(ctx context.Context, p time.Duration, fn func(context.Context)) {
	t := time.NewTicker(p)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}
func (d *daemon) register(ctx context.Context, id string) error {
	var workspaces []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := d.api.Do(ctx, "GET", "/api/daemon/workspaces", nil, &workspaces); err != nil {
		return err
	}
	d.mu.Lock()
	d.workspaceCount = len(workspaces)
	d.mu.Unlock()
	var harnesses []string
	for h := range d.cfg.Harnesses {
		harnesses = append(harnesses, h)
	}
	sort.Strings(harnesses)
	for _, ws := range workspaces {
		var runtimes []struct {
			ID      uuid.UUID `json:"id"`
			OwnerID uuid.UUID `json:"owner_id"`
			Harness string    `json:"harness"`
		}
		if err := d.api.Do(ctx, "POST", "/api/daemon/register", map[string]any{"workspace_id": ws.ID, "daemon_id": id, "device_name": d.cfg.Device, "harnesses": harnesses, "versions": d.versions, "platform": d.platform}, &runtimes); err != nil {
			return err
		}
		d.mu.Lock()
		if d.ownerOf == nil {
			d.ownerOf = make(map[uuid.UUID]uuid.UUID)
		}
		d.knownWorkspaces[ws.ID] = true
		for _, r := range runtimes {
			d.ownerOf[r.ID] = r.OwnerID
			d.setHarness(r.ID, r.Harness)
			d.refs = append(d.refs, runtimeRef{WorkspaceID: ws.ID, RuntimeID: r.ID})
		}
		d.mu.Unlock()
	}
	return nil
}
func (d *daemon) heartbeat(ctx context.Context) {
	if err := d.refreshReferences(ctx); err != nil {
		slog.Warn("runtime refresh pending", "err", err)
	}
	refs := d.references()
	metrics, metricsErr := Machine(ctx, d.cfg.Home)
	d.mu.Lock()
	models := d.models
	d.mu.Unlock()
	bases := map[string]string{}
	if d.bases != nil {
		bases = d.bases.States()
	}
	bases["sandbox"] = "ready"
	if d.sandboxErr != nil {
		bases["sandbox"] = envs.Failed(d.sandboxErr)
	}
	for i := 0; i < len(refs); i += 64 {
		body := map[string]any{"runtimes": refs[i:min(i+64, len(refs))], "models": models}
		if metricsErr == nil {
			body["metrics"] = metrics
		}
		if bases != nil {
			body["bases"] = bases
		}
		if err := d.api.Do(ctx, "POST", "/api/daemon/heartbeat", body, nil); err != nil {
			slog.Warn("heartbeat failed", "err", err)
		}
	}
}
func (d *daemon) claim(ctx context.Context) {
	if d.sandboxErr != nil {
		return
	}
	refs := d.references()
	if len(refs) == 0 {
		return
	}
	d.claimCoresearchWork(ctx, refs)
	d.claimTurns(ctx, refs, 64, true)
	d.mu.Lock()
	free := d.cfg.Slots - (len(d.active) - len(d.coreActive))
	d.mu.Unlock()
	d.claimTurns(ctx, refs, free, false)
}
func (d *daemon) claimTurns(ctx context.Context, currentRefs []runtimeRef, free int, coreOnly bool) {
	if free <= 0 || len(currentRefs) == 0 {
		return
	}

	// Rotate the first batch so a busy early workspace cannot starve later ones.
	d.rotation %= len(currentRefs)
	refs := append(append([]runtimeRef{}, currentRefs[d.rotation:]...), currentRefs[:d.rotation]...)
	d.rotation = (d.rotation + 64) % len(currentRefs)
	for i := 0; i < len(refs) && free > 0 && ctx.Err() == nil; i += 64 {
		var got []claimed
		body := map[string]any{"runtimes": refs[i:min(i+64, len(refs))], "slots": free, "coresearch_only": coreOnly}
		if runtime.GOOS == "linux" {
			body["guest_isolation"] = agent.GuestIsolationProtocol
		}
		if err := d.api.Do(ctx, "POST", "/api/daemon/agent-runs/claim", body, &got); err != nil {
			slog.Warn("claim failed", "err", err)
			var he *apiclient.HTTPError
			if errors.As(err, &he) && he.Status == 404 {
				if err := d.refreshReferences(ctx); err != nil {
					slog.Warn("runtime refresh pending", "err", err)
				}
			}
			return
		}
		if len(got) > free {
			d.fatal(errors.New("server claimed more than available slots"))
			return
		}
		for _, c := range got {
			if (c.Coresearch != nil) != coreOnly {
				d.fatal(errors.New("server returned a run outside requested claim mode"))
				return
			}
			if c.AgentRun.RuntimeID == nil || c.AgentRun.Attempt <= 0 || !strings.HasPrefix(c.Token, "ltk_") {
				d.fatal(errors.New("invalid claimed run identity or role token"))
				return
			}
			key := scratchName(c.AgentRun.ID, c.AgentRun.Attempt)
			d.mu.Lock()
			duplicate := d.active[key]
			d.active[key] = true
			if coreOnly {
				if d.coreActive == nil {
					d.coreActive = map[string]bool{}
				}
				d.coreActive[key] = true
			}
			d.mu.Unlock()
			if duplicate {
				d.fatal(errors.New("duplicate claimed attempt"))
				return
			}
			free--
			runCtx, stop := context.WithCancel(ctx)
			d.mu.Lock()
			if d.activeRefs == nil {
				d.activeRefs = map[string]runtimeRef{}
				d.activeCancels = map[string]context.CancelFunc{}
			}
			d.activeRefs[key] = runtimeRef{c.AgentRun.WorkspaceID, *c.AgentRun.RuntimeID}
			d.activeCancels[key] = stop
			d.mu.Unlock()
			d.wg.Add(1)
			go func(c claimed, key string) {
				defer d.wg.Done()
				defer stop()
				defer func() {
					d.mu.Lock()
					delete(d.active, key)
					delete(d.coreActive, key)
					delete(d.activeRefs, key)
					delete(d.activeCancels, key)
					d.mu.Unlock()
					d.signal()
				}()
				d.fatal(d.runAgentRun(runCtx, c))
			}(c, key)
		}
	}
}
func (d *daemon) hints(ctx context.Context) {
	u := "ws" + strings.TrimPrefix(strings.TrimRight(d.cfg.Server, "/"), "http") + "/api/daemon/ws"
	for ctx.Err() == nil {
		dialer := *websocket.DefaultDialer
		dialer.HandshakeTimeout = 10 * time.Second
		c, resp, err := dialer.DialContext(ctx, u, http.Header{"Authorization": {"Bearer " + d.cfg.Token}})
		if resp != nil && err != nil {
			resp.Body.Close()
		}
		if err == nil {
			c.SetReadLimit(64 << 10)
			done := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					c.Close()
				case <-done:
				}
			}()
			for {
				var msg struct {
					Type string `json:"type"`
				}
				if err = c.ReadJSON(&msg); err != nil {
					break
				}
				if msg.Type == "ready" || msg.Type == "agent_run_available" || msg.Type == "coresearch.work.queued" {
					d.signal()
				}
			}
			close(done)
			c.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func (d *daemon) flush(ctx context.Context) error {
	if err := d.flushCoresearch(ctx); err != nil {
		return err
	}
	if err := d.messages.Flush(ctx); err != nil {
		return err
	}
	return d.out.Flush(ctx)
}

var errMessagesPending = errors.New("final report awaits durable message acknowledgements")

func (d *daemon) send(ctx context.Context, r Report) error {
	pending, err := d.messages.pending(r)
	if err != nil {
		return err
	}
	if pending {
		return errMessagesPending
	}
	if r.Kind == "complete" && r.HandIn != nil {
		probs, err := d.postHandIn(ctx, r, r.HandIn)
		var refused *apiclient.HTTPError
		switch {
		case errors.Is(err, errLabContextChanged):
			r.HandIn = nil
			r.Kind, r.FailureReason, r.Error = "fail", agent.ReasonLabContextChanged, err.Error()
		case errors.As(err, &refused) && !waitsForServer(err):
			// The attempt is no longer one that takes a hand-in (swept, or
			// cancelled): its report still settles it, as a machine failure.
			r.HandIn = nil
			r.Kind, r.FailureReason, r.Error = "fail", agent.ReasonRuntimeOffline, err.Error()
		case err != nil:
			return err
		}
		// The session that could correct the answer is gone: the run is
		// tried again, and the agent never hears why.
		if len(probs) > 0 {
			r.Kind, r.FailureReason = "fail", agent.ReasonRuntimeOffline
			r.Error = "the hand-in kept while the server was unreachable had problems:\n" + strings.Join(probs, "\n")
		}
	}
	if r.Kind == "rerun" {
		return d.api.Do(ctx, "POST", "/api/daemon/reruns/"+r.AgentRunID.String()+"/result", map[string]any{"workspace_id": r.WorkspaceID, "runtime_id": r.RuntimeID, "attempt": r.Attempt,
			"steps": r.Rerun.Steps, "outputs": r.Rerun.Outputs, "failure": r.Rerun.Failure}, nil)
	}
	if err := d.api.Do(ctx, "POST", "/api/daemon/agent-runs/"+r.AgentRunID.String()+"/"+r.Kind, reportBody(r), nil); err != nil {
		return err
	}
	// Only once the server holds the failure: a pass reported before it
	// would leave the machine flagged.
	if r.Kind == "fail" {
		d.mu.Lock()
		harness := d.harnessOf[r.RuntimeID]
		d.mu.Unlock()
		d.recheck(harness, r.FailureReason)
	}
	return nil
}
func reportBody(r Report) map[string]any {
	return map[string]any{"workspace_id": r.WorkspaceID, "runtime_id": r.RuntimeID, "attempt": r.Attempt, "usage": r.Usage, "naming_usage": r.NamingUsage, "session_id": r.SessionID, "error": r.Error, "failure_reason": r.FailureReason, "mode": r.Mode, "policy_digest": r.PolicyDigest, "model": r.Model}
}
func (d *daemon) sendMessages(ctx context.Context, r Report) error {
	return d.api.Do(ctx, "POST", "/api/daemon/agent-runs/"+r.AgentRunID.String()+"/messages", messageBody(r), nil)
}
func scratchName(id uuid.UUID, attempt int32) string { return fmt.Sprintf("%s-%d", id, attempt) }

// holdScratch gives a scratch folder to one holder at a time: a run of its
// scope, or gc deciding whether to delete it.
func (d *daemon) holdScratch(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.scratches[name] {
		return false
	}
	if d.scratches == nil {
		d.scratches = map[string]bool{}
	}
	d.scratches[name] = true
	return true
}
func (d *daemon) releaseScratch(name string) {
	d.mu.Lock()
	delete(d.scratches, name)
	d.mu.Unlock()
}

// Only a finished record written after child exit starts the deletion clock.
func (d *daemon) gc(context.Context) error {
	if err := d.gcChats(); err != nil {
		return err
	}
	es, err := os.ReadDir(d.cfg.Home)
	if err != nil {
		return err
	}
	for _, e := range es {
		if !e.IsDir() {
			continue
		}
		d.mu.Lock()
		active := d.active[e.Name()]
		d.mu.Unlock()
		// Holding the folder while deciding and deleting keeps a run of its
		// scope from reopening it half-deleted.
		if active || !d.holdScratch(e.Name()) {
			continue
		}
		err := collectScratch(filepath.Join(d.cfg.Home, e.Name()), finishedRecord(d.cfg.StateDir, e.Name()))
		d.releaseScratch(e.Name())
		if err != nil {
			return err
		}
	}
	return nil
}

// collectScratch deletes a folder whose finished record is old enough: at
// once for an attempt's own folder, which its run would have removed had the
// daemon not stopped; for a scope's folder, whose sessions later runs resume,
// the retention its record keeps, or a day when it keeps none. The record
// goes with the folder.
func collectScratch(dir, record string) error {
	reservation, err := agent.ReserveScratch(dir)
	if errors.Is(err, agent.ErrScratchBusy) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer reservation.Release()
	if strings.HasPrefix(filepath.Base(dir), agent.RetiredScratchPrefix) {
		return reservation.Remove()
	}

	fi, err := os.Lstat(record)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	var keep time.Duration
	if strings.HasPrefix(filepath.Base(dir), "scope-") {
		keep = 24 * time.Hour
		var m struct {
			RetentionDays int `json:"retention_days"`
		}
		if b, err := readBounded(record, recordLimit); err == nil && json.Unmarshal(b, &m) == nil && m.RetentionDays > 0 && m.RetentionDays <= maxRetentionDays {
			keep = time.Duration(m.RetentionDays) * 24 * time.Hour
		}
	}
	if time.Since(fi.ModTime()) < keep {
		return nil
	}
	if err := reservation.Remove(); err != nil {
		return err
	}
	return os.Remove(record)
}

// finishedRecord is the daemon's record that a folder's run finished, kept
// in its state folder: a run's code may write its run folder, so nothing
// there is trusted.
func finishedRecord(stateDir, dir string) string {
	return filepath.Join(stateDir, "finished", filepath.Base(dir))
}

// markFinished writes a folder's finished record.
func (d *daemon) markFinished(dir string, v any) error {
	return atomicJSON(filepath.Dir(finishedRecord(d.cfg.StateDir, dir)), filepath.Base(dir), v)
}

// unmarkFinished takes the record away, as a reused folder runs again.
func (d *daemon) unmarkFinished(dir string) error {
	if err := os.Remove(finishedRecord(d.cfg.StateDir, dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
