package flow

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func manualTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}
func (l *lab) manualNote(t *testing.T, card *uuid.UUID, deadline any) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := l.env.Owner.Exec(context.Background(), `INSERT INTO inbox_item(id,workspace_id,user_id,kind,text,title,deadline,card_id) VALUES($1,$2,$3,'manual_decision','','Remember',$4,$5)`, id, l.ws, l.owner, deadline, card)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (l *lab) manualExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := l.env.Owner.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func (l *lab) manualSweep(t *testing.T, at time.Time) {
	t.Helper()
	if err := l.e.sweepDecisions(context.Background(), at); err != nil {
		t.Fatal(err)
	}
}
func (l *lab) manualReminderCount(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM event WHERE type='decision.reminder' AND payload->>'inbox_item_id'=$1 AND recipient_id=$2 AND card_id IS NULL`, id.String(), l.owner).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestManualDecisionReminderLifecycle(t *testing.T) {
	l := newLab(t)
	for _, tc := range []struct{ name, zone, deadline, boundary string }{
		{"local midnight", "Europe/Copenhagen", "2026-11-05", "2026-11-03T23:00:00Z"},
		{"spring DST", "Europe/Copenhagen", "2026-03-30", "2026-03-28T23:00:00Z"},
		{"autumn DST", "Europe/Copenhagen", "2026-10-26", "2026-10-24T22:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l.manualExec(t, `UPDATE "user" SET timezone=$2 WHERE id=$1`, l.owner, tc.zone)
			id := l.manualNote(t, nil, tc.deadline)
			at := manualTime(t, tc.boundary)
			l.manualSweep(t, at.Add(-time.Nanosecond))
			if n := l.manualReminderCount(t, id); n != 0 {
				t.Fatalf("early: %d", n)
			}
			l.manualSweep(t, at)
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			deadlineDay, err := time.ParseInLocation("2006-01-02", tc.deadline, loc)
			if err != nil {
				t.Fatal(err)
			}
			for _, boundary := range []time.Time{deadlineDay, deadlineDay.AddDate(0, 0, 1)} {
				for _, offset := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
					l.manualSweep(t, boundary.Add(offset))
					if status, _, _, _ := l.decision(t, id); status != "open" {
						t.Fatalf("at %s: status %s", boundary.Add(offset), status)
					}
					if n := l.manualReminderCount(t, id); n != 1 {
						t.Fatalf("at %s: reminders %d", boundary.Add(offset), n)
					}
				}
			}
			if n := l.manualReminderCount(t, id); n != 1 {
				t.Fatalf("reminders: %d", n)
			}
			l.manualExec(t, `UPDATE inbox_item SET deadline=NULL WHERE id=$1`, id)
			l.manualSweep(t, at.Add(72*time.Hour))
			l.manualExec(t, `UPDATE inbox_item SET deadline='2020-01-01' WHERE id=$1`, id)
			l.manualSweep(t, at.Add(96*time.Hour))
			if n := l.manualReminderCount(t, id); n != 1 {
				t.Fatalf("rearmed: %d", n)
			}
			if status, _, _, _ := l.decision(t, id); status != "open" {
				t.Fatalf("status: %s", status)
			}
		})
	}
	t.Run("unsent deadline and timezone edits", func(t *testing.T) {
		l.manualExec(t, `UPDATE "user" SET timezone='UTC' WHERE id=$1`, l.owner)
		id := l.manualNote(t, nil, nil)
		at := manualTime(t, "2026-11-04T12:00:00Z")
		l.manualSweep(t, at)
		l.manualExec(t, `UPDATE inbox_item SET deadline='2026-11-06' WHERE id=$1`, id)
		l.manualSweep(t, at)
		if n := l.manualReminderCount(t, id); n != 0 {
			t.Fatal(n)
		}
		l.manualExec(t, `UPDATE "user" SET timezone='Pacific/Auckland' WHERE id=$1`, l.owner)
		l.manualSweep(t, at)
		if n := l.manualReminderCount(t, id); n != 1 {
			t.Fatalf("timezone not rechecked: %d", n)
		}
	})
	t.Run("late creation", func(t *testing.T) {
		for _, deadline := range []string{"2026-11-04", "2026-01-01"} {
			id := l.manualNote(t, nil, deadline)
			l.manualSweep(t, manualTime(t, "2026-11-04T12:00:00Z"))
			if n := l.manualReminderCount(t, id); n != 1 {
				t.Fatal(n)
			}
		}
	})
	t.Run("triage", func(t *testing.T) {
		for _, field := range []string{"archived_at", "snoozed_until"} {
			id := l.manualNote(t, nil, "2026-01-01")
			l.manualExec(t, `UPDATE inbox_item SET `+field+`='2030-01-01' WHERE id=$1`, id)
			l.manualSweep(t, manualTime(t, "2026-11-04T12:00:00Z"))
			if n := l.manualReminderCount(t, id); n != 0 {
				t.Fatal(n)
			}
		}
	})
}
func TestManualDecisionReminderRace(t *testing.T) {
	l := newLab(t)
	id := l.manualNote(t, nil, "2026-01-01")
	at := manualTime(t, "2026-11-04T12:00:00Z")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- l.e.sweepDecisions(context.Background(), at) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := l.manualReminderCount(t, id); n != 1 {
		t.Fatal(n)
	}
	id = l.manualNote(t, nil, "2026-01-01")
	l.manualExec(t, `UPDATE inbox_item SET status='decided',resolved_at=now() WHERE id=$1`, id)
	l.manualSweep(t, at)
	if n := l.manualReminderCount(t, id); n != 0 {
		t.Fatal(n)
	}
}
func TestManualDecisionReminderResolveRace(t *testing.T) {
	l := newLab(t)
	ctx := context.Background()
	at := manualTime(t, "2026-11-04T12:00:00Z")
	for _, change := range []string{"status='decided',resolved_at=now()", "deadline='2030-01-01'"} {
		id := l.manualNote(t, nil, "2026-01-01")
		tx, err := l.env.Owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT id FROM inbox_item WHERE id=$1 FOR UPDATE`, id); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			sent, err := l.st.Global().RemindManualDecision(ctx, db.RemindManualDecisionParams{WorkspaceID: l.ws, ID: id, At: at})
			if err == nil && sent {
				err = fmt.Errorf("reminded after committed close/edit")
			}
			result <- err
		}()
		waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		tick := time.NewTicker(time.Millisecond)
		for {
			var query string
			err = l.env.Owner.QueryRow(waitCtx, `SELECT COALESCE((SELECT query FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) LIMIT 1),'')`, tx.Conn().PgConn().PID()).Scan(&query)
			if err != nil {
				tick.Stop()
				cancel()
				t.Fatal(err)
			}
			if query != "" {
				if !strings.Contains(query, "remind_manual_decision") {
					tick.Stop()
					cancel()
					t.Fatalf("unexpected blocked query: %s", query)
				}
				break
			}
			select {
			case err := <-result:
				tick.Stop()
				cancel()
				t.Fatalf("reminder completed before row lock released: %v", err)
			case <-waitCtx.Done():
				tick.Stop()
				cancel()
				t.Fatal("reminder never contended on held row: ", waitCtx.Err())
			case <-tick.C:
			}
		}
		tick.Stop()
		cancel()
		if _, err = tx.Exec(ctx, `UPDATE inbox_item SET `+change+` WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-result; err != nil {
			t.Fatal(err)
		}
		if n := l.manualReminderCount(t, id); n != 0 {
			t.Fatal(n)
		}
	}
}

func TestManualDecisionSurvivesStudyEndAndSweep(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	id := l.manualNote(t, &c.ID, "2026-01-01")
	before := l.card(t, c.ID)
	runs := l.runs(t, c.ID)
	l.in(t, func(tx *store.Tx) error { return service.EndStudyItems(context.Background(), tx, c.ID, store.System) })
	l.manualExec(t, `UPDATE inbox_item SET touched_at='2020-01-01' WHERE id=$1`, id)
	l.manualSweep(t, manualTime(t, "2026-11-04T12:00:00Z"))
	if status, _, _, _ := l.decision(t, id); status != "open" {
		t.Fatal(status)
	}
	if !reflect.DeepEqual(before, l.card(t, c.ID)) || !reflect.DeepEqual(runs, l.runs(t, c.ID)) {
		t.Fatal("manual sweep changed study or pending runs")
	}
	for _, ending := range []string{"stopped", "done"} {
		l.manualExec(t, `UPDATE card SET ending=$2 WHERE id=$1`, c.ID, ending)
		before = l.card(t, c.ID)
		l.in(t, func(tx *store.Tx) error { return service.EndStudyItems(context.Background(), tx, c.ID, store.System) })
		l.manualSweep(t, manualTime(t, "2026-11-04T12:00:00Z"))
		if status, _, _, _ := l.decision(t, id); status != "open" {
			t.Fatalf("%s: %s", ending, status)
		}
		if !reflect.DeepEqual(before, l.card(t, c.ID)) || !reflect.DeepEqual(runs, l.runs(t, c.ID)) {
			t.Fatalf("%s: study/runs changed", ending)
		}
	}
	l.manualExec(t, `UPDATE inbox_item SET status='dismissed',resolved_at=now() WHERE id=$1`, id)
	l.manualSweep(t, manualTime(t, "2026-11-04T12:00:00Z"))
	var archived *time.Time
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT archived_at FROM inbox_item WHERE id=$1`, id).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived != nil {
		t.Fatal("manual history auto archived")
	}
	l.manualExec(t, `DELETE FROM card WHERE id=$1`, c.ID)
	var n int
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM inbox_item WHERE id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("note survived study deletion")
	}
}

// Candidates are advisory; delivery must recheck the committed row rather
// than trusting what the scheduler selected before a human mutation.
func TestManualDecisionReminderCommittedRecheck(t *testing.T) {
	l := newLab(t)
	ctx := context.Background()
	at := manualTime(t, "2026-11-04T12:00:00Z")
	for _, tc := range []struct{ name, change string }{
		{"resolved", "status='decided',resolved_at=now()"},
		{"dismissed", "status='dismissed',resolved_at=now()"},
		{"deadline moved", "deadline='2030-01-01'"},
		{"deadline removed", "deadline=NULL"},
		{"archived", "archived_at=now()"},
		{"snoozed", "snoozed_until='2030-01-01'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := l.manualNote(t, nil, "2026-01-01")
			due, err := l.st.Global().DueManualReminders(ctx, at)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, r := range due {
				if r.ID == id {
					found = true
					if r.UserID != l.owner {
						t.Fatal("wrong creator")
					}
				}
			}
			if !found {
				t.Fatal("candidate missing")
			}
			l.manualExec(t, `UPDATE inbox_item SET `+tc.change+` WHERE id=$1`, id)
			sent, err := l.st.Global().RemindManualDecision(ctx, db.RemindManualDecisionParams{WorkspaceID: l.ws, ID: id, At: at})
			if err != nil {
				t.Fatal(err)
			}
			if sent || l.manualReminderCount(t, id) != 0 {
				t.Fatal("stale candidate delivered")
			}
		})
	}
	t.Run("membership lost", func(t *testing.T) {
		c := l.study(t, "hypotheses", true, nil)
		id := l.manualNote(t, &c.ID, "2026-01-01")
		l.manualExec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, l.ws, l.owner)
		sent, err := l.st.Global().RemindManualDecision(ctx, db.RemindManualDecisionParams{WorkspaceID: l.ws, ID: id, At: at})
		if err != nil {
			t.Fatal(err)
		}
		if sent || l.manualReminderCount(t, id) != 0 {
			t.Fatal("hidden note reminded")
		}
	})
}
