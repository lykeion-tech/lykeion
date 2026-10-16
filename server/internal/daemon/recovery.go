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
		if err := d.recoverAttempt(ctx, r, finals[e.Name()]); err != nil {
			return err
		}
	}
	return d.flush(ctx)
}

func (d *daemon) recoverAttempt(ctx context.Context, r Report, hasFinal bool) error {
	names := []struct {
		name      string
		retention int
	}{{r.Scratch, r.RetentionDays}}
	if r.RetainedScratch != "" {
		names = append(names, struct {
			name      string
			retention int
		}{r.RetainedScratch, r.RetainedRetentionDays})
	}
	for _, entry := range names {
		name := entry.name
		if name == "" {
			name = scratchName(r.AgentRunID, r.Attempt)
		}
		if name != filepath.Base(name) || name == "." || name == ".." || (name != scratchName(r.AgentRunID, r.Attempt) && !validScopeScratch(name)) {
			return errors.New("journaled scratch is not an owned folder name")
		}
	}
	for _, entry := range names {
		name := entry.name
		if name == "" {
			name = scratchName(r.AgentRunID, r.Attempt)
		}
		if name != filepath.Base(name) || name == "." || name == ".." {
			return errors.New("journaled scratch is not a folder name")
		}
		dir := filepath.Join(d.cfg.Home, name)
		if info, err := os.Lstat(dir); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("journaled scratch is not a directory")
			}
			reservation, err := agent.ReserveScratch(dir)
			if err != nil {
				return err
			}
			defer reservation.Release()
			if err := agent.RecoverScratch(dir); err != nil {
				return fmt.Errorf("recover attempt %s: %w", r.AgentRunID, err)
			}
			if err := atomicJSON(dir, ".finished", map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt, "retention_days": entry.retention}); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := d.flush(ctx); err != nil {
		return err
	}
	if !hasFinal {
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
	return nil
}

func validScopeScratch(name string) bool {
	if !strings.HasPrefix(name, "scope-") || len(name) != 70 {
		return false
	}
	for _, ch := range name[6:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}
