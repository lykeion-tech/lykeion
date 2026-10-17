package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/middleware"
	"lykeion/server/pkg/agent"
)

func (d *daemon) runAgentRun(ctx context.Context, c claimed) (localErr error) {
	r := Report{AgentRunID: c.AgentRun.ID, WorkspaceID: c.AgentRun.WorkspaceID, RuntimeID: *c.AgentRun.RuntimeID, Attempt: c.AgentRun.Attempt, Kind: "complete"}
	dir := filepath.Join(d.cfg.Home, scratchName(r.AgentRunID, r.Attempt))
	created := false
	reused := false
	var retainedDir string
	var cont *continuity
	var final agent.Result
	fail := func(err error) { r.Kind = "fail"; r.Error = err.Error(); r.FailureReason = agent.ReasonAgentError }
	// Registered before the deferred report, so it runs after the finished
	// marker: gc never finds the folder unheld with only an older marker.
	var held string
	var reservation *agent.ScratchReservation
	var freshReservation *agent.ScratchReservation
	defer func() {
		reservation.Release()
		freshReservation.Release()
		if held != "" {
			d.releaseScratch(held)
		}
	}()

	// Journal before the server can enter running or any CLI can launch.
	journalRecord := r
	journalRecord.Kind = "fail"
	journalRecord.FailureReason = agent.ReasonRuntimeOffline
	if err := d.journal().Put(journalRecord); err != nil {
		return err
	}
	defer func() {
		if ctx.Err() != nil {
			r.Kind = "fail"
			r.Error = ctx.Err().Error()
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		if err := d.out.Put(r); err != nil {
			localErr = errors.Join(localErr, err)
			return
		}
		// Only after the final report is durable may a baseline become resumable.
		localErr = errors.Join(localErr, d.finishContinuity(cont, final))
		if retainedDir != "" {
			if err := atomicJSON(retainedDir, ".finished", map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt, "retention_days": journalRecord.RetainedRetentionDays}); err != nil {
				localErr = errors.Join(localErr, err)
				return
			}
		}
		if created {
			if err := atomicJSON(dir, ".finished", map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt, "retention_days": journalRecord.RetentionDays}); err != nil {
				localErr = errors.Join(localErr, err)
				return
			}
		}
		localErr = errors.Join(localErr, d.forgetAttempt(r))
	}()
	// The attempt keeps this snapshot even if the policy file changes later.
	pol, err := LoadPolicy(d.cfg.StateDir, c.AgentRun.WorkspaceID)
	if err != nil {
		fail(fmt.Errorf("workspace policy: %w", err))
		return nil
	}
	r.Mode, r.PolicyDigest = runMode(c, pol), pol.Digest
	if r.Mode == ModePrivate && c.AgentRun.ContinuityScope != "" {
		if !continuityScope.MatchString(c.AgentRun.ContinuityScope) {
			fail(fmt.Errorf("invalid continuity scope %q", c.AgentRun.ContinuityScope))
			return nil
		}
		// A private scope keeps one folder, whose .cli-home holds the CLI's
		// sessions. A run that finds it held waits for it a while, then works in
		// its attempt's own folder.
		name := privateScratch(c)
		var ok bool
		if ok, err = d.waitScratch(ctx, name); err != nil {
			fail(err)
			return nil
		}
		if ok {
			held, r.Scratch, journalRecord.Scratch, journalRecord.RetentionDays = name, name, name, pol.RetentionDays
			dir = filepath.Join(d.cfg.Home, name)
			// Attempt metadata changes before launch; delivery reports stay immutable.
			journal := d.journal()
			if err = atomicJSON(journal.Dir, filepath.Base(journal.file(journalRecord)), journalRecord); err != nil {
				fail(fmt.Errorf("journal scope scratch: %w", err))
				return err
			}
		}
	}
	if held == "" {
		err = os.Mkdir(dir, 0700)
	} else {
		if err = os.Mkdir(dir, 0700); err == nil || errors.Is(err, os.ErrExist) {
			reused = errors.Is(err, os.ErrExist)
			reservation, err = agent.ReserveScratch(dir)
			if err == nil {
				err = reuseScratch(dir)
			}
		}
	}
	if err != nil {
		fail(fmt.Errorf("prepare scratch: %w", err))
		return nil
	}
	created = true
	prompt := c.AgentRun.Prompt
	if c.AgentRun.Skill != "" {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(c.AgentRun.Skill), 0600); err != nil {
			fail(err)
			return nil
		}
		prompt = "Read and follow the skill in SKILL.md in your working directory.\n\n" + prompt
	}
	backend, err := d.cfg.NewBackend(c.AgentRun.Harness, d.cfg.Harnesses[c.AgentRun.Harness])
	if err != nil {
		fail(err)
		return nil
	}
	base := "/api/daemon/agent-runs/" + r.AgentRunID.String()
	ref := reportBody(r)
	if err = d.api.Do(ctx, "POST", base+"/prepare-lease", ref, nil); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	if err = d.api.Do(ctx, "POST", base+"/start", ref, nil); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watched := make(chan struct{})
	go func(ref Report) { defer close(watched); d.watchCancel(runCtx, cancel, base, ref) }(r)
	defer func() { cancel(); <-watched }()
	self, err := os.Executable()
	if err != nil {
		fail(err)
		return nil
	}
	var sinkErr error
	var sequence int32
	sink := func(m agent.Message) error {
		if sequence == math.MaxInt32 {
			sinkErr = errors.New("message sequence exhausted")
			return sinkErr
		}
		sequence++
		sinkErr = d.persistMessage(r, sequence, m)
		return sinkErr
	}
	opts := agent.ExecOptions{ScratchReservation: reservation, OnMessage: sink, Dir: dir, MCPURL: c.MCPURL, MCPToken: c.Token, MCP: c.MCP, Resume: c.AgentRun.SessionID, Model: c.AgentRun.RequestedModel, Env: []string{
		"LYK_TOKEN=" + c.Token, "LYK_SERVER=" + d.cfg.Server, "LYK_CONFIG=" + filepath.Join(dir, ".lyk-config.json"), "PATH=" + filepath.Dir(self) + string(os.PathListSeparator) + os.Getenv("PATH"),
	}}
	if cont, err = d.beginContinuity(c, pol); err != nil {
		fail(err)
		return nil
	}
	note := func(text string) error {
		return sink(agent.Message{Type: "status", Content: rawJSON(map[string]string{"type": "continuity", "message": text})})
	}
	if cont != nil {
		opts.Inherit, opts.Resume, opts.ClaudeBaseline = true, cont.resume, cont.baseline
		if cont.note != "" {
			if err = note(cont.note); err != nil {
				fail(err)
				return errors.Join(err, d.abandonContinuity(cont))
			}
		}
	}
	// Resume only what this run's CLI can find; otherwise continue fresh.
	if opts.Resume != "" && !agent.SessionReachable(c.AgentRun.Harness, opts.Resume, opts.Inherit, dir) {
		if err = note("session " + opts.Resume + " is not in the CLI's session store; starting a fresh session"); err != nil {
			fail(err)
			return errors.Join(err, d.abandonContinuity(cont))
		}
		opts.Resume, opts.ClaudeBaseline = "", nil
	}
	// A private Claude resume is charged against its folder's usage baseline;
	// with none for this session, continue fresh rather than fail at launch.
	if opts.Resume != "" && !opts.Inherit && c.AgentRun.Harness == "claude" && !agent.ClaudeResumable(dir, opts.Resume) {
		if err = note("session " + opts.Resume + " has no usage baseline in this run's folder; starting a fresh session"); err != nil {
			fail(err)
			return errors.Join(err, d.abandonContinuity(cont))
		}
		opts.Resume, opts.ClaudeBaseline = "", nil
	}

	// execute returns the launch error, or the result and the stream error.
	execute := func() (res agent.Result, streamErr, launchErr error) {
		sess, err := backend.Execute(runCtx, prompt, opts)
		if err != nil {
			return res, nil, err
		}
		streamErr = d.stream(r, sess.Messages, cancel)
		res, ok := <-sess.Done
		if !ok {
			res.Err = errors.New("backend closed without a result")
		}
		return res, streamErr, nil
	}
	res, streamErr, launchErr := execute()
	var discovery *agent.PrelaunchDiscoveryError
	if launchErr != nil && errors.As(launchErr, &discovery) && reused && held != "" && r.Mode == ModePrivate && !opts.Inherit && runCtx.Err() == nil && sinkErr == nil {
		if err = agent.RecoverScratch(dir); err != nil {
			fail(err)
			return nil
		}
		freshDir := filepath.Join(d.cfg.Home, scratchName(r.AgentRunID, r.Attempt))
		// Exclusive creation precedes journaling so a collision never becomes
		// recovery-owned. No credentials are written until the journal is durable.
		if err = os.Mkdir(freshDir, 0700); err != nil {
			fail(err)
			return nil
		}
		freshReservation, err = agent.ReserveScratch(freshDir)
		if err != nil {
			fail(err)
			return nil
		}
		if err = atomicJSON(freshDir, ".finished", map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt}); err != nil {
			fail(err)
			return err
		}
		candidate := journalRecord
		candidate.RetainedScratch, candidate.RetainedRetentionDays = held, journalRecord.RetentionDays
		candidate.Scratch, candidate.RetentionDays = "", 0
		if err = atomicJSON(d.journal().Dir, filepath.Base(d.journal().file(candidate)), candidate); err != nil {
			fail(err)
			return err
		}
		journalRecord = candidate
		retainedDir, dir = dir, freshDir
		r.Scratch, r.RetainedScratch, r.RetainedRetentionDays = "", held, journalRecord.RetainedRetentionDays
		if err = os.Remove(filepath.Join(dir, ".finished")); err != nil {
			fail(err)
			return nil
		}
		if c.AgentRun.Skill != "" {
			if err = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(c.AgentRun.Skill), 0600); err != nil {
				fail(err)
				return nil
			}
		}
		opts.Dir, opts.ScratchReservation, opts.Resume, opts.ClaudeBaseline = dir, freshReservation, "", nil
		for i, entry := range opts.Env {
			if strings.HasPrefix(entry, "LYK_CONFIG=") {
				opts.Env[i] = "LYK_CONFIG=" + filepath.Join(dir, ".lyk-config.json")
			}
		}
		if err = note("private discovery configuration was refused; restarting in a fresh folder without prior session or files"); err != nil {
			fail(err)
			return err
		}
		if err = runCtx.Err(); err != nil {
			fail(err)
			return nil
		}
		res, streamErr, launchErr = execute()
	}
	if launchErr != nil {
		fail(launchErr)
		return d.abandonContinuity(cont)
	}
	// A refused resume did no model work, so one fresh session is safe.
	if cont != nil && opts.Resume != "" && res.ResumeRefused && streamErr == nil && sinkErr == nil && runCtx.Err() == nil {
		if err = note("the CLI refused to resume session " + opts.Resume + "; starting a fresh session"); err == nil {
			opts.Resume, opts.ClaudeBaseline = "", nil
			if res, streamErr, launchErr = execute(); launchErr != nil {
				fail(launchErr)
				final.Err = launchErr
				return nil
			}
		}
	}
	final = res
	if final.Err == nil && streamErr != nil {
		final.Err = streamErr
	}
	if sinkErr != nil {
		var oversized *oversizedMessageError
		if !errors.As(sinkErr, &oversized) {
			localErr = errors.Join(localErr, sinkErr)
		}
	}
	r.Usage, r.SessionID, r.Model = res.Usage, res.SessionID, res.Model
	if res.Err != nil {
		fail(res.Err)
		r.FailureReason = res.Reason
		if r.FailureReason == "" {
			r.FailureReason = agent.ReasonAgentError
		}
	}
	if streamErr != nil {
		fail(streamErr)
		var oversized *oversizedMessageError
		if !errors.As(streamErr, &oversized) {
			localErr = streamErr
		}
	}
	if runCtx.Err() != nil && ctx.Err() == nil {
		// The ACK is informational; durable replay carries final usage.
		if err = d.api.Do(ctx, "POST", base+"/cancel-ack", ref, nil); err != nil {
			slog.Warn("cancellation acknowledgement pending", "run", r.AgentRunID, "attempt", r.Attempt, "err", err)
		}
	}
	return localErr
}
func rawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func messageBody(r Report) map[string]any {
	return map[string]any{"workspace_id": r.WorkspaceID, "runtime_id": r.RuntimeID, "attempt": r.Attempt, "messages": r.Messages}
}

type oversizedMessageError struct {
	seq  int32
	size int
}

func (e *oversizedMessageError) Error() string {
	return fmt.Sprintf("message %d encoded size %d exceeds %d bytes; original evidence retained in daemon state", e.seq, e.size, middleware.MaxDaemonMessageBytes)
}

// stream persists byte-bounded batches before any send and drains through child
// exit even during cancellation. Network outages cannot discard its final batch.
func (d *daemon) stream(r Report, msgs <-chan agent.Message, cancel context.CancelFunc) error {
	r.Kind = "messages"
	var pending []wireMsg
	var seq int32
	var firstErr error
	flush := func() {
		if len(pending) == 0 {
			return
		}
		r.Messages = pending
		if err := d.messages.Put(r); err != nil {
			// Oversize is a run failure with preserved evidence. Persistence
			// failure must additionally stop the daemon and reach its caller.
			var oversized *oversizedMessageError
			if firstErr == nil || errors.As(firstErr, &oversized) {
				firstErr = err
			}
			cancel()
		}
		pending = nil
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				flush()
				return firstErr
			}
			if seq == math.MaxInt32 {
				if firstErr == nil {
					firstErr = errors.New("message sequence exhausted")
				}
				cancel()
				continue
			}
			seq++
			content := m.Content
			if len(content) == 0 {
				content = json.RawMessage(`null`)
			}
			wm := wireMsg{Seq: seq, Type: m.Type, Tool: m.Tool, Content: content}
			candidate := append(pending, wm)
			r.Messages = candidate
			b, err := json.Marshal(messageBody(r))
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				cancel()
				continue
			}
			if len(b) > middleware.MaxDaemonMessageBytes {
				flush()
				r.Messages = []wireMsg{wm}
				b, err = json.Marshal(messageBody(r))
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					cancel()
					continue
				}
				if len(b) > middleware.MaxDaemonMessageBytes {
					evidenceDir := filepath.Join(d.cfg.StateDir, "oversized-messages")
					if err := atomicJSON(evidenceDir, fmt.Sprintf("%s-%010d-%010d.json", r.AgentRunID, r.Attempt, seq), r); err != nil {
						firstErr = err
					} else if firstErr == nil {
						firstErr = &oversizedMessageError{seq: seq, size: len(b)}
					}
					cancel()
					continue
				}
				pending = []wireMsg{wm}
			} else {
				pending = candidate
			}
			if len(pending) >= 50 {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}
func (d *daemon) watchCancel(ctx context.Context, cancel context.CancelFunc, base string, r Report) {
	q := url.Values{"workspace_id": {r.WorkspaceID.String()}, "runtime_id": {r.RuntimeID.String()}, "attempt": {strconv.Itoa(int(r.Attempt))}}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var s struct{ Status string }
			err := d.api.Do(ctx, "GET", base+"/status?"+q.Encode(), nil, &s)
			var he *apiclient.HTTPError
			if (err == nil && s.Status != "running") || (errors.As(err, &he) && (he.Status == 404 || he.Status == 409)) {
				cancel()
				return
			}
		}
	}
}

// The CLI has not started: ambiguous control-plane acknowledgements are safe
// to settle as runtime loss, fenced by attempt, without rerunning the command.
func retryableControlLoss(err error) bool {
	var response *apiclient.HTTPError
	if errors.As(err, &response) {
		return response.Status == 408 || response.Status == 429 || response.Status >= 500
	}
	var transport *url.Error
	return errors.As(err, &transport) || errors.Is(err, context.DeadlineExceeded)
}

// The real adapter invokes this synchronously before accepting its next record.
func (d *daemon) persistMessage(r Report, seq int32, m agent.Message) error {
	r.Kind = "messages"
	content := m.Content
	if len(content) == 0 {
		content = json.RawMessage(`null`)
	}
	r.Messages = []wireMsg{{Seq: seq, Type: m.Type, Tool: m.Tool, Content: content}}
	b, err := json.Marshal(messageBody(r))
	if err != nil {
		return err
	}
	if len(b) > middleware.MaxDaemonMessageBytes {
		dir := filepath.Join(d.cfg.StateDir, "oversized-messages")
		if err := atomicJSON(dir, fmt.Sprintf("%s-%010d-%010d.json", r.AgentRunID, r.Attempt, seq), r); err != nil {
			return err
		}
		return &oversizedMessageError{seq: seq, size: len(b)}
	}
	return d.messages.Put(r)
}

// privateScratch names the folder every private run of one scope shares: the
// scope's key, a sha256 of workspace, responsible user, harness and scope, so
// the scope's "/" and ":" never reach a path and another harness or user is
// another folder.
func privateScratch(c claimed) string {
	run := c.AgentRun
	return "scope-" + sessionScope{Workspace: run.WorkspaceID, User: run.ResponsibleUserID, Harness: run.Harness, Scope: run.ContinuityScope}.key()
}

// reuseScratch makes a scope's folder or reopens it as it is: the CLI's state,
// its sessions and the agent's files stay; the files the daemon writes for one
// run go, so each run places its own.
func reuseScratch(dir string) error {
	if err := privateDir(dir); err != nil {
		return err
	}
	for _, name := range []string{".finished", "SKILL.md", ".lyk-config.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
