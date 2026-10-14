package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lykeion/server/pkg/agent"
)

func (d *daemon) journal() *Outbox { return &Outbox{Dir: filepath.Join(d.cfg.StateDir, "attempts")} }
func (d *daemon) forgetAttempt(r Report) error {
	j := d.journal()
	if err := os.Remove(j.file(r)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(j.Dir)
}

// Recover before registration revives the persistent runtime identity. Existing
// reports remain authoritative; lost attempts get a fenced runtime-loss report.
func (d *daemon) recoverAttempts(ctx context.Context) error {
	if err := d.recoverSessions(); err != nil {
		return fmt.Errorf("recover session registry: %w", err)
	}
	j := d.journal()
	if err := privateDir(j.Dir); err != nil {
		return err
	}
	if err := j.recover(); err != nil {
		return err
	}
	entries, err := os.ReadDir(j.Dir)
	if err != nil {
		return err
	}
	if err := privateDir(d.out.Dir); err != nil {
		return err
	}
	if err := d.out.recover(); err != nil {
		return err
	}
	finals := map[string]bool{}
	// Snapshot all durable finals before any replay can acknowledge/remove one.
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		_, err := readReport(filepath.Join(d.out.Dir, e.Name()))
		if errors.Is(err, os.ErrNotExist) {
			_, err = readReport(filepath.Join(d.out.Dir, "quarantine", e.Name()))
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		finals[e.Name()] = err == nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := readReport(filepath.Join(j.Dir, e.Name()))
		if err != nil {
			return err
		}
		if j.file(r) != filepath.Join(j.Dir, e.Name()) {
			return errors.New("attempt journal identity mismatch")
		}
		dir := filepath.Join(d.cfg.Home, scratchName(r.AgentRunID, r.Attempt))
		if info, err := os.Lstat(dir); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("journaled scratch is not a directory")
			}
			if err := agent.RecoverScratch(dir); err != nil {
				return fmt.Errorf("recover attempt %s: %w", e.Name(), err)
			}
			if err := atomicJSON(dir, ".finished", map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt}); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := d.flush(ctx); err != nil {
			return err
		}
		if !finals[e.Name()] {
			r.Kind = "fail"
			r.FailureReason = agent.ReasonRuntimeOffline
			r.Error = "daemon process exited before final report"
			if err := d.out.Put(r); err != nil {
				return err
			}
		}

		if err := d.forgetAttempt(r); err != nil {
			return err
		}
	}
	return d.flush(ctx)
}
