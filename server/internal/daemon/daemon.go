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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

type Config struct {
	Server, Token, StateDir, Home, Device string
	Harnesses                             map[string]string
	Slots                                 int
	Poll, Heartbeat, Flush                time.Duration
	NewBackend                            func(harness, bin string) (agent.Backend, error)
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
}
func Detect() map[string]string {
	out := map[string]string{}
	for _, h := range []string{"claude", "codex"} {
		if p, err := exec.LookPath(h); err == nil {
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
	AgentRun struct {
		ID          uuid.UUID  `json:"id"`
		WorkspaceID uuid.UUID  `json:"workspace_id"`
		RuntimeID   *uuid.UUID `json:"runtime_id"`
		Attempt     int32      `json:"attempt"`
		Harness     string     `json:"harness"`
		Prompt      string     `json:"prompt"`
		Skill       string     `json:"skill"`
		SessionID   string     `json:"session_id"`
	} `json:"agent_run"`
	Token  string            `json:"token"`
	MCPURL string            `json:"mcp_url"`
	MCP    []agent.MCPServer `json:"mcp"`
}
type daemon struct {
	cfg           Config
	api           *apiclient.Client
	refs          []runtimeRef
	out, messages *Outbox
	wake          chan struct{}
	failures      chan error
	mu            sync.Mutex
	active        map[string]bool
	wg            sync.WaitGroup
	rotation      int
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
	if err = privateDir(cfg.Home); err != nil {
		return err
	}
	id, err := daemonID(cfg.StateDir, cfg.Server)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := &daemon{cfg: cfg, api: &apiclient.Client{Server: cfg.Server, Token: cfg.Token, HTTP: &http.Client{Timeout: 30 * time.Second}}, wake: make(chan struct{}, 1), failures: make(chan error, 1), active: map[string]bool{}}
	d.messages = &Outbox{Dir: filepath.Join(cfg.StateDir, "messages"), Send: d.sendMessages}
	d.out = &Outbox{Dir: filepath.Join(cfg.StateDir, "outbox"), Send: d.send}
	if err = d.register(ctx, id); err != nil {
		return err
	}
	if err = d.flush(ctx); err != nil {
		return err
	}
	background := func(fn func()) { d.wg.Add(1); go func() { defer d.wg.Done(); fn() }() }
	background(func() { d.every(ctx, cfg.Heartbeat, d.heartbeat) })
	background(func() { d.every(ctx, cfg.Flush, func(ctx context.Context) { d.fatal(d.flush(ctx)) }) })
	background(func() { d.every(ctx, time.Hour, func(ctx context.Context) { d.fatal(d.gc(ctx)) }) })
	background(func() { d.hints(ctx) })
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
	// A concurrent child storage failure must not disappear during shutdown.
	select {
	case err := <-d.failures:
		runErr = errors.Join(runErr, err)
	default:
	}
	return runErr
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
	var harnesses []string
	for h := range d.cfg.Harnesses {
		harnesses = append(harnesses, h)
	}
	sort.Strings(harnesses)
	for _, ws := range workspaces {
		var runtimes []struct {
			ID uuid.UUID `json:"id"`
		}
		if err := d.api.Do(ctx, "POST", "/api/daemon/register", map[string]any{"workspace_id": ws.ID, "daemon_id": id, "device_name": d.cfg.Device, "harnesses": harnesses}, &runtimes); err != nil {
			return err
		}
		for _, r := range runtimes {
			d.refs = append(d.refs, runtimeRef{WorkspaceID: ws.ID, RuntimeID: r.ID})
		}
	}
	return nil
}
func (d *daemon) heartbeat(ctx context.Context) {
	for i := 0; i < len(d.refs); i += 64 {
		if err := d.api.Do(ctx, "POST", "/api/daemon/heartbeat", map[string]any{"runtimes": d.refs[i:min(i+64, len(d.refs))]}, nil); err != nil {
			slog.Warn("heartbeat failed", "err", err)
		}
	}
}
func (d *daemon) claim(ctx context.Context) {
	d.mu.Lock()
	free := d.cfg.Slots - len(d.active)
	d.mu.Unlock()
	if free <= 0 || len(d.refs) == 0 {
		return
	}
	// Rotate the first batch so a busy early workspace cannot starve later ones.
	refs := append(append([]runtimeRef{}, d.refs[d.rotation:]...), d.refs[:d.rotation]...)
	d.rotation = (d.rotation + 64) % len(d.refs)
	for i := 0; i < len(refs) && free > 0 && ctx.Err() == nil; i += 64 {
		var got []claimed
		if err := d.api.Do(ctx, "POST", "/api/daemon/agent-runs/claim", map[string]any{"runtimes": refs[i:min(i+64, len(refs))], "slots": free}, &got); err != nil {
			slog.Warn("claim failed", "err", err)
			return
		}
		if len(got) > free {
			d.fatal(errors.New("server claimed more than available slots"))
			return
		}
		for _, c := range got {
			if c.AgentRun.RuntimeID == nil || c.AgentRun.Attempt <= 0 || !strings.HasPrefix(c.Token, "ltk_") {
				d.fatal(errors.New("invalid claimed run identity or role token"))
				return
			}
			key := scratchName(c.AgentRun.ID, c.AgentRun.Attempt)
			d.mu.Lock()
			duplicate := d.active[key]
			d.active[key] = true
			d.mu.Unlock()
			if duplicate {
				d.fatal(errors.New("duplicate claimed attempt"))
				return
			}
			free--
			d.wg.Add(1)
			go func(c claimed, key string) {
				defer d.wg.Done()
				defer func() { d.mu.Lock(); delete(d.active, key); d.mu.Unlock(); d.signal() }()
				d.fatal(d.runAgentRun(ctx, c))
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
				if msg.Type == "ready" || msg.Type == "agent_run_available" {
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
	return d.api.Do(ctx, "POST", "/api/daemon/agent-runs/"+r.AgentRunID.String()+"/"+r.Kind, reportBody(r), nil)
}
func reportBody(r Report) map[string]any {
	return map[string]any{"workspace_id": r.WorkspaceID, "runtime_id": r.RuntimeID, "attempt": r.Attempt, "usage": r.Usage, "session_id": r.SessionID, "error": r.Error, "failure_reason": r.FailureReason}
}
func (d *daemon) sendMessages(ctx context.Context, r Report) error {
	return d.api.Do(ctx, "POST", "/api/daemon/agent-runs/"+r.AgentRunID.String()+"/messages", messageBody(r), nil)
}
func scratchName(id uuid.UUID, attempt int32) string { return fmt.Sprintf("%s-%d", id, attempt) }

// Only a finished marker written after child exit starts the deletion clock.
func (d *daemon) gc(context.Context) error {
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
		if active {
			continue
		}
		dir := filepath.Join(d.cfg.Home, e.Name())
		fi, err := os.Lstat(filepath.Join(dir, ".finished"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() || time.Since(fi.ModTime()) < 24*time.Hour {
			continue
		}
		if err = os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}
