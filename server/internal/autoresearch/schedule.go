package autoresearch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// MaxLateness is how late a schedule may fire. A later slot is recorded as a
// skipped run with reason late.
const MaxLateness = 5 * time.Minute

// Zone is an IANA time zone. Local is refused: it means whatever zone the
// server happens to run in.
func Zone(tz string) (*time.Location, error) {
	if tz == "" || tz == "Local" {
		return nil, errors.New("an IANA time zone such as Europe/Copenhagen is required")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", tz)
	}
	return loc, nil
}

// parseSchedule reads a five-field cron expression, or a descriptor such as
// @daily, to be evaluated in the zone tz. The zone comes only from tz, so
// CRON_TZ= and TZ= prefixes are refused, and so is @every, which is an
// interval rather than a calendar schedule.
func parseSchedule(expr, tz string) (cron.Schedule, *time.Location, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" || len(expr) > 100 || strings.Contains(expr, "TZ=") || strings.HasPrefix(expr, "@every") {
		return nil, nil, fmt.Errorf("cron must be five fields or a descriptor such as @daily, not %q", expr)
	}
	loc, err := Zone(tz)
	if err != nil {
		return nil, nil, err
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, nil, fmt.Errorf("cron %q: %v", expr, err)
	}
	return sched, loc, nil
}

// NextRun is the schedule's first slot after the given time, in UTC.
func NextRun(expr, tz string, after time.Time) (time.Time, error) {
	sched, loc, err := parseSchedule(expr, tz)
	if err != nil {
		return time.Time{}, err
	}
	next := sched.Next(after.In(loc))
	if next.IsZero() {
		return next, fmt.Errorf("cron %q never fires", expr)
	}
	return next.UTC(), nil
}

// latestSlot is the most recent slot at or before now, starting from the one
// that fell due: after downtime only the latest missed slot fires.
func latestSlot(sched cron.Schedule, loc *time.Location, due, now time.Time) time.Time {
	slot := due
	for {
		n := sched.Next(slot.In(loc))
		if n.IsZero() || n.After(now) {
			return slot
		}
		slot = n
	}
}

// Tick fires due schedules and dispatches again the runs a crash left queued.
func (s *Service) Tick(ctx context.Context) error {
	q := s.Store.Global()
	due, err := q.AutoresearchDue(ctx, s.now())
	if err != nil {
		return err
	}
	for _, d := range due {
		if err := s.fire(ctx, d.WorkspaceID, d.TriggerID); err != nil {
			slog.Error("autoresearch schedule", "trigger", d.TriggerID, "err", err)
		}
	}
	stale, err := q.AutoresearchStaleRuns(ctx, s.now())
	if err != nil {
		return err
	}
	for _, r := range stale {
		if err := s.Dispatch(ctx, r.WorkspaceID, r.RunID); err != nil {
			slog.Error("autoresearch repair", "run", r.RunID, "err", err)
		}
	}
	if err := s.Deliver(ctx); err != nil {
		slog.Error("webhook deliveries", "err", err)
	}
	return s.Sync(ctx)
}

// fire evaluates one due schedule under the workspace lock: it advances
// next_run_at, admits the latest missed slot once, as a late skip if it is too
// old, and dispatches it.
func (s *Service) fire(ctx context.Context, ws, trigger uuid.UUID) error {
	var run *db.AutoresearchRun
	err := s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if err := lockWorkspace(ctx, tx); err != nil {
			return err
		}
		t, err := tx.Q.LockTrigger(ctx, db.LockTriggerParams{WorkspaceID: ws, ID: trigger})
		if err != nil {
			return err
		}
		ap, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: ws, ID: t.AutoresearchID})
		if err != nil {
			return err
		}
		now := s.now()
		// Another server may have fired it while this one waited for the lock.
		if !t.Enabled || ap.Status != "active" || t.NextRunAt == nil || t.NextRunAt.After(now) {
			return nil
		}
		sched, loc, err := parseSchedule(t.Cron, t.Tz)
		if err == nil && sched.Next(now.In(loc)).IsZero() {
			err = fmt.Errorf("cron %q never fires", t.Cron)
		}
		if err != nil {
			if _, err := tx.Q.SetTriggerEnabled(ctx, db.SetTriggerEnabledParams{Enabled: false, WorkspaceID: ws, ID: t.ID}); err != nil {
				return err
			}
			return tx.Emit(ctx, "autoresearch_trigger.disabled", nil, store.System,
				map[string]any{"autoresearch_id": ap.ID, "trigger_id": t.ID, "reason": err.Error()})
		}
		planned := latestSlot(sched, loc, *t.NextRunAt, now).UTC()
		next := sched.Next(now.In(loc)).UTC()
		if err := tx.Q.SetTriggerNextRun(ctx, db.SetTriggerNextRunParams{NextRunAt: &next, WorkspaceID: ws, ID: t.ID}); err != nil {
			return err
		}
		a := Admission{Autoresearch: ap.ID, Trigger: &t.ID, Source: "schedule", Actor: t.CreatedBy, PlannedAt: planned,
			Key: "schedule:" + t.ID.String() + ":" + planned.Format(time.RFC3339)}
		if now.Sub(planned) > MaxLateness {
			a.Status, a.Reason = "skipped", "late"
		}
		r, created, err := s.Admit(ctx, tx, a)
		if err != nil {
			return err
		}
		if created && r.Status == "queued" {
			run = &r
		}
		return nil
	})
	if err != nil || run == nil {
		return err
	}
	return s.Dispatch(ctx, ws, run.ID)
}
