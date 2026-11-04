package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

// maxHandInReasks is how many times a run is asked to correct its hand-in.
const maxHandInReasks = 2

var errLabContextChanged = errors.New("lab evidence changed during the turn")

// maxOutputBytes is the server's limit on one output; a larger file is never sent.
const maxOutputBytes = 100 << 20

// maxOutputProblems bounds the lines refused outputs add to a re-ask.
const maxOutputProblems = 20

// handIn saves the run's outputs, then posts its final answer as the hand-in.
// While the server finds problems, the agent is asked up to twice, in the same
// session, to correct them. It returns the last turn's result, with every
// turn's usage, and the problems still unresolved. A turn that fails ends the
// asking: its result carries the error and no problems are returned. The error
// is the daemon's own: a call to the server that did not get an answer.
func (d *daemon) handIn(ctx context.Context, c claimed, res agent.Result, exec func(prompt, resume string) agent.Result, say func(agent.Message) error) (agent.Result, []string, error) {
	role := &apiclient.Client{Server: d.cfg.Server, Token: c.Token, HTTP: &http.Client{}}
	// Cardless Aristotle and Autoresearch runs submit only their hand-ins.
	outputs := c.AgentRun.Kind != "autoresearch" && c.AgentRun.Kind != "aristotle"
	// The saved outputs are listed only once there is a file to send, so a
	// run without outputs needs the server only for the hand-in itself.
	var saved map[string]bool
	listSaved := func() (map[string]bool, error) {
		if saved == nil {
			var err error
			saved, err = savedOutputs(ctx, role, c.AgentRun.ID)
			return saved, err
		}
		return saved, nil
	}
	dir := filepath.Join(d.cfg.Home, scratchName(c.AgentRun.ID, c.AgentRun.Attempt))
	var runtimeID uuid.UUID
	if c.AgentRun.RuntimeID != nil {
		runtimeID = *c.AgentRun.RuntimeID
	}
	attempt := Report{AgentRunID: c.AgentRun.ID, WorkspaceID: c.AgentRun.WorkspaceID, RuntimeID: runtimeID, Attempt: c.AgentRun.Attempt}
	for asked := 0; ; asked++ {
		var probs []string
		var err error
		if outputs {
			if probs, err = sendOutputs(ctx, role, dir, listSaved, nil); err != nil {
				return res, nil, err
			}
		}
		// A refused output is fixed before the hand-in, which is kept once accepted.
		if len(probs) == 0 {
			attempt.HandInMessageSeq = res.OutputMessageSeq
			if probs, err = d.postHandIn(ctx, attempt, handInJSON(res.Output)); err != nil {
				if errors.Is(err, errLabContextChanged) {
					return res, nil, err
				}
				return res, nil, &unsentHandIn{err}
			}
		}
		if len(probs) == 0 || asked == maxHandInReasks {
			return res, probs, nil
		}
		if err := say(agent.Message{Type: "status", Content: rawJSON(map[string]any{"handin_reask": asked + 1, "problems": probs})}); err != nil {
			return res, nil, err
		}
		next := exec(reaskPrompt(probs), res.SessionID)
		next.Usage = addUsage(res.Usage, next.Usage)
		// A session that can't be resumed can't correct its hand-in.
		if next.ResumeRefused {
			res.Usage = next.Usage
			return res, probs, nil
		}
		if next.SessionID == "" {
			next.SessionID = res.SessionID
		}
		res = next
		if res.Err != nil {
			return res, nil, nil
		}
	}
}

func reaskPrompt(probs []string) string {
	var b strings.Builder
	b.WriteString("Lykeion could not accept your hand-in:\n")
	for _, p := range probs {
		b.WriteString("- " + p + "\n")
	}
	b.WriteString("Fix these and give your hand-in again.")
	return b.String()
}

// unsentHandIn is a hand-in post that got no verdict: the answer is whole,
// and only the server is missing.
type unsentHandIn struct{ err error }

func (e *unsentHandIn) Error() string { return e.err.Error() }
func (e *unsentHandIn) Unwrap() error { return e.err }

// waitsForServer reports whether a failed post is one Multica's daemon
// retries a terminal report for: no answer from the server, a 5xx, 408 or
// 429. A post the daemon itself stopped counts too; the caller tells a
// stopping daemon, whose answer is kept, from a cancelled run, whose is not.
func waitsForServer(err error) bool {
	var e *apiclient.HTTPError
	if !errors.As(err, &e) {
		return true
	}
	return e.Status >= 500 || e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests
}

// handInJSON is the run's final answer as a hand-in. An answer that is not
// JSON is sent as a string, so the server names the problem.
func handInJSON(output string) json.RawMessage {
	handIn := json.RawMessage(output)
	if !json.Valid(handIn) {
		handIn, _ = json.Marshal(output)
	}
	return handIn
}

// postHandIn posts handIn as attempt's hand-in and returns the server's
// problems with it.
func (d *daemon) postHandIn(ctx context.Context, attempt Report, handIn json.RawMessage) ([]string, error) {
	body := map[string]any{"workspace_id": attempt.WorkspaceID, "runtime_id": attempt.RuntimeID, "attempt": attempt.Attempt, "hand_in": handIn}
	if attempt.HandInMessageSeq > 0 {
		body["message_seq"] = attempt.HandInMessageSeq
	}
	var reply struct {
		FreshAttemptRequired bool     `json:"fresh_attempt_required"`
		Accepted             bool     `json:"accepted"`
		Problems             []string `json:"problems"`
	}
	endpoint := "/api/daemon/agent-runs/" + attempt.AgentRunID.String() + "/hand-in"
	if err := d.api.Do(ctx, "POST", endpoint, body, &reply); err != nil {
		var httpErr *apiclient.HTTPError
		if attempt.HandInMessageSeq <= 0 || !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadRequest {
			return nil, err
		}
		delete(body, "message_seq")
		if err = d.api.Do(ctx, "POST", endpoint, body, &reply); err != nil {
			return nil, err
		}
	}
	if reply.FreshAttemptRequired {
		return nil, errLabContextChanged
	}
	if !reply.Accepted && len(reply.Problems) == 0 {
		return []string{"the hand-in was not accepted"}, nil
	}
	if reply.Accepted {
		return nil, nil
	}
	return reply.Problems, nil
}

// savedOutputs are the names of the outputs the run has already saved.
func savedOutputs(ctx context.Context, role *apiclient.Client, run uuid.UUID) (map[string]bool, error) {
	var recs []struct {
		AgentRunID *uuid.UUID `json:"agent_run_id"`
		Body       struct {
			Name string `json:"name"`
		} `json:"body"`
	}
	if err := role.Do(ctx, "GET", "/api/role/records?kind=output", nil, &recs); err != nil {
		return nil, err
	}
	saved := map[string]bool{}
	for _, r := range recs {
		if r.AgentRunID != nil && *r.AgentRunID == run {
			saved[r.Body.Name] = true
		}
	}
	return saved, nil
}

// sendOutputs saves each regular file under the run folder's outputs folder
// not yet among the saved ones, which listSaved gives when first needed,
// under its path in the run folder. Links are never followed. A file the
// server's limits refuse is a problem for the agent. sums, when set, gets
// each saved file's sha256 as the server took it, by name.
func sendOutputs(ctx context.Context, role *apiclient.Client, dir string, listSaved func() (map[string]bool, error), sums map[string]string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// Only a real folder is walked: a link there could reach the run
	// folder's own configuration.
	if info, err := root.Lstat("outputs"); errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var probs []string
	more, refused := 0, false
	problem := func(p string) {
		if len(probs) < maxOutputProblems-1 {
			probs = append(probs, p)
		} else {
			more++
		}
	}
	err = fs.WalkDir(root.FS(), "outputs", func(name string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() {
			return err
		}
		saved, err := listSaved()
		if err != nil || saved[name] {
			return err
		}
		// After one refusal the server's caps are reached; the rest are counted.
		if refused {
			more++
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxOutputBytes {
			problem(name + " was not saved: an output can be at most 100 MB")
			return nil
		}
		// A hard link can be a second name for a file outside the outputs folder.
		if linkCount(info) > 1 {
			problem(name + " was not saved: it is a hard link to another file")
			return nil
		}
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		// The file opened must be the one the walk saw, not a link put in its place.
		if now, err := f.Stat(); err != nil || !os.SameFile(now, info) {
			problem(name + " was not saved: it changed while being saved")
			return nil
		}
		var took struct{ Sha256 string }
		err = role.Upload(ctx, "/api/role/outputs", map[string]string{"path": name}, "file", path.Base(name), f, &took)
		var refusal *apiclient.HTTPError
		if errors.As(err, &refusal) && (refusal.Status == http.StatusBadRequest || refusal.Status == http.StatusRequestEntityTooLarge) {
			refused = true
			problem(name + " was not saved: " + strings.TrimPrefix(refusal.Msg, "POST /api/role/outputs: "))
			return nil
		}
		if err == nil {
			saved[name] = true
			if sums != nil {
				sums[name] = took.Sha256
			}
		}
		return err
	})
	if more > 0 {
		probs = append(probs, fmt.Sprintf("and %d more files were not saved", more))
	}
	return probs, err
}

// addUsage is a and b together, one entry per model. A cost is kept only when
// both sides report one; otherwise the server prices the tokens.
func addUsage(a, b []agent.Usage) []agent.Usage {
	out := append([]agent.Usage(nil), a...)
	for _, u := range b {
		i := 0
		for i < len(out) && out[i].Model != u.Model {
			i++
		}
		if i == len(out) {
			out = append(out, u)
			continue
		}
		e := &out[i]
		e.InputTokens += u.InputTokens
		e.OutputTokens += u.OutputTokens
		e.CacheReadTokens += u.CacheReadTokens
		e.CacheWriteTokens += u.CacheWriteTokens
		if e.CostUSD != nil && u.CostUSD != nil {
			cost := *e.CostUSD + *u.CostUSD
			e.CostUSD = &cost
		} else {
			e.CostUSD = nil
		}
	}
	return out
}
