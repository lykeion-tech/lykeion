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
	"time"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/middleware"
	"lykeion/server/pkg/agent"
)

func (d *daemon) runAgentRun(ctx context.Context, c claimed) (localErr error) {
	r := Report{AgentRunID: c.AgentRun.ID, WorkspaceID: c.AgentRun.WorkspaceID, RuntimeID: *c.AgentRun.RuntimeID, Attempt: c.AgentRun.Attempt, Kind: "complete"}
	dir := filepath.Join(d.cfg.Home, scratchName(r.AgentRunID, r.Attempt))
	created := false
	fail := func(err error) { r.Kind = "fail"; r.Error = err.Error(); r.FailureReason = agent.ReasonAgentError }
	// Persist even setup/start failures; finalization happens only after Done.
	defer func() {
		if ctx.Err() != nil {
			r.Kind = "fail"
			r.Error = ctx.Err().Error()
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		localErr = errors.Join(localErr, d.out.Put(r))
		if created {
			localErr = errors.Join(localErr, atomicJSON(dir, ".finished", map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt}))
		}
	}()
	if err := os.Mkdir(dir, 0700); err != nil {
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
		return nil
	}
	if err = d.api.Do(ctx, "POST", base+"/start", ref, nil); err != nil {
		fail(err)
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
	sess, err := backend.Execute(runCtx, prompt, agent.ExecOptions{Dir: dir, MCPURL: c.MCPURL, MCPToken: c.Token, MCP: c.MCP, Resume: c.AgentRun.SessionID, Env: []string{
		"LYK_TOKEN=" + c.Token, "LYK_SERVER=" + d.cfg.Server, "LYK_CONFIG=" + filepath.Join(dir, ".lyk-config.json"), "PATH=" + filepath.Dir(self) + string(os.PathListSeparator) + os.Getenv("PATH"),
	}})
	if err != nil {
		fail(err)
		return nil
	}
	streamErr := d.stream(r, sess.Messages, cancel)
	res, ok := <-sess.Done
	if !ok {
		fail(errors.New("backend closed without a result"))
		return streamErr
	}
	r.Usage, r.SessionID = res.Usage, res.SessionID
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
