package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

// Report contains no credential. Attempt and sequence identify immutable evidence.
type Report struct {
	AgentRunID  uuid.UUID `json:"agent_run_id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	RuntimeID   uuid.UUID `json:"runtime_id"`
	Attempt     int32     `json:"attempt"`
	// Scratch is the scope folder a private attempt used; empty means the
	// attempt's own folder, scratchName(AgentRunID, Attempt).
	Scratch string `json:"scratch,omitempty"`
	// RetainedScratch preserves the refused scope while Scratch identifies execution.
	RetainedScratch       string `json:"retained_scratch,omitempty"`
	RetainedRetentionDays int    `json:"retained_retention_days,omitempty"`
	// RetentionDays is how long after its last run gc keeps a scope folder,
	// from the workspace policy the attempt started under.
	RetentionDays int `json:"retention_days,omitempty"`

	Kind          string        `json:"kind"`
	Usage         []agent.Usage `json:"usage,omitempty"`
	NamingUsage   []agent.Usage `json:"naming_usage,omitempty"`
	SessionID     string        `json:"session_id,omitempty"`
	Error         string        `json:"error,omitempty"`
	FailureReason string        `json:"failure_reason,omitempty"`
	Mode          string        `json:"mode,omitempty"`
	PolicyDigest  string        `json:"policy_digest,omitempty"`
	Model         string        `json:"model,omitempty"`
	Messages      []wireMsg     `json:"messages,omitempty"`
	// HandIn is a completed attempt's final answer the server has not yet
	// taken: it is posted as the hand-in before the report itself.
	HandIn           json.RawMessage `json:"hand_in,omitempty"`
	HandInMessageSeq int32           `json:"hand_in_message_seq,omitempty"`
	// Rerun is a rerun's result, posted in place of a completion: a report
	// of kind "rerun" settles its run through the rerun's result route.
	Rerun      *rerunResult `json:"rerun,omitempty"`
	Rejections []time.Time  `json:"rejections,omitempty"`
}
type wireMsg struct {
	Seq     int32           `json:"seq"`
	Type    string          `json:"type"`
	Tool    string          `json:"tool"`
	Content json.RawMessage `json:"content"`
}
type Outbox struct {
	Dir  string
	Send func(context.Context, Report) error
	mu   sync.Mutex
}

func Permanent(err error) bool {
	var e *apiclient.HTTPError
	return errors.As(err, &e) && e.Status >= 400 && e.Status < 500 && e.Status != 401 && e.Status != 408 && e.Status != 429
}
func (o *Outbox) file(r Report) string {
	name := fmt.Sprintf("%s-%010d", r.AgentRunID, r.Attempt)
	if r.Kind == "messages" && len(r.Messages) > 0 {
		name += fmt.Sprintf("-%010d", r.Messages[0].Seq)
	}
	return filepath.Join(o.Dir, name+".json")
}
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a private directory: %s", dir)
	}
	return os.Chmod(dir, 0700)
}
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// atomicJSON faithfully retains the original queue's file and directory fsync boundary.
func atomicJSON(dir, name string, v any) error {
	if err := privateDir(dir); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDir(dir)
}
func sameReport(a, b Report) bool {
	a.Rejections = nil
	b.Rejections = nil
	ab, ae := json.Marshal(a)
	bb, be := json.Marshal(b)
	return ae == nil && be == nil && bytes.Equal(ab, bb)
}
func readReport(path string) (Report, error) {
	var r Report
	fi, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	if !fi.Mode().IsRegular() {
		return r, fmt.Errorf("nonregular queue record %s", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	if err == nil && (r.AgentRunID == uuid.Nil || r.Attempt <= 0 || (r.Kind != "complete" && r.Kind != "fail" && r.Kind != "messages" && (r.Kind != "rerun" || r.Rerun == nil))) {
		err = errors.New("invalid queue record")
	}
	return r, err
}
func (o *Outbox) Put(r Report) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.AgentRunID == uuid.Nil || r.Attempt <= 0 {
		return errors.New("positive attempt and run required")
	}
	if err := privateDir(o.Dir); err != nil {
		return err
	}
	for _, path := range []string{o.file(r), filepath.Join(o.Dir, "quarantine", filepath.Base(o.file(r)))} {
		if old, err := readReport(path); err == nil {
			if !sameReport(old, r) {
				return errors.New("conflicting durable report")
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return atomicJSON(o.Dir, filepath.Base(o.file(r)), r)
}

// recover preserves incomplete and conflicting files as evidence and reports them.
func (o *Outbox) recover() error {
	es, err := os.ReadDir(o.Dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range es {
		if !strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		p := filepath.Join(o.Dir, e.Name())
		r, err := readReport(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		target := o.file(r)
		if !strings.HasPrefix(e.Name(), "."+filepath.Base(target)+"-") {
			errs = append(errs, errors.New("temporary record identity mismatch"))
			continue
		}
		if old, err := readReport(target); err == nil {
			if !sameReport(old, r) {
				errs = append(errs, errors.New("conflicting recovery record"))
				continue
			}
			err = os.Remove(p)
			errs = append(errs, err)
		} else if errors.Is(err, os.ErrNotExist) {
			if err = os.Chmod(p, 0600); err == nil {
				err = os.Rename(p, target)
			}
			errs = append(errs, err)
		} else {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, syncDir(o.Dir))...)
}

// Flush serializes recovery, sending, rejection updates and ACK removal. Network
// errors leave durable records pending; local storage errors reach the caller.
func (o *Outbox) Flush(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := privateDir(o.Dir); err != nil {
		return err
	}
	recoveryErr := o.recover()
	es, err := os.ReadDir(o.Dir)
	if err != nil {
		return errors.Join(recoveryErr, err)
	}
	errs := []error{recoveryErr}
	for _, e := range es {
		if ctx.Err() != nil {
			break
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(o.Dir, e.Name())
		r, err := readReport(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if o.file(r) != p {
			errs = append(errs, errors.New("queue record identity mismatch"))
			continue
		}
		if o.Send == nil {
			errs = append(errs, errors.New("queue sender unavailable"))
			continue
		}
		err = o.Send(ctx, r)
		if err == nil {
			if err = os.Remove(p); err == nil {
				err = syncDir(o.Dir)
			}
			errs = append(errs, err)
			continue
		}
		if Permanent(err) {
			now := time.Now().UTC()
			r.Rejections = append(r.Rejections, now)
			// Keep the first rejection and two latest observations; bounded disk metadata.
			if len(r.Rejections) > 3 {
				r.Rejections = []time.Time{r.Rejections[0], r.Rejections[len(r.Rejections)-2], now}
			}
			if err = atomicJSON(o.Dir, e.Name(), r); err != nil {
				errs = append(errs, err)
				continue
			}
			if len(r.Rejections) >= 3 && now.Sub(r.Rejections[0]) >= 10*time.Minute {
				q := filepath.Join(o.Dir, "quarantine")
				if err = privateDir(q); err == nil {
					target := filepath.Join(q, e.Name())
					existing, readErr := readReport(target)
					switch {
					case readErr == nil && !sameReport(existing, r):
						err = errors.New("quarantine conflicts with pending evidence")
					case readErr == nil:
						err = os.Remove(p)
					case errors.Is(readErr, os.ErrNotExist):
						err = os.Rename(p, target)
					default:
						err = readErr
					}
				}
				if err == nil {
					err = errors.Join(syncDir(q), syncDir(o.Dir))
				}
				errs = append(errs, err)
				if err == nil {
					slog.Error("durable evidence quarantined after permanent rejection", "run", r.AgentRunID, "attempt", r.Attempt, "kind", r.Kind)
				}
			}
		} else {
			slog.Warn("durable delivery pending", "run", r.AgentRunID, "attempt", r.Attempt, "err", err)
		}
	}
	return errors.Join(errs...)
}
func (o *Outbox) pending(r Report) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := os.ReadDir(o.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	prefix := fmt.Sprintf("%s-%010d-", r.AgentRunID, r.Attempt)
	for _, e := range es {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".json") {
			return true, nil
		}
	}
	return false, nil
}
