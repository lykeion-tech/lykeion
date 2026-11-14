package autoresearch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	db "lykeion/server/pkg/db/generated"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextRunReadsTheZoneAcrossDaylightSaving(t *testing.T) {
	for _, c := range []struct{ expr, tz, after, want string }{
		{"0 9 * * *", "Europe/Copenhagen", "2026-10-13T06:59:00Z", "2026-10-13T07:00:00Z"}, // 09:00 CEST
		{"0 9 * * *", "Europe/Copenhagen", "2026-10-24T12:00:00Z", "2026-10-25T08:00:00Z"}, // 09:00 CET, the day the clocks go back
		{"0 9 * * *", "Europe/Copenhagen", "2026-03-28T12:00:00Z", "2026-03-29T07:00:00Z"},
		{"@daily", "America/New_York", "2026-10-13T12:00:00Z", "2026-10-14T04:00:00Z"},
		{"*/15 * * * *", "UTC", "2026-10-13T06:00:00Z", "2026-10-13T06:15:00Z"},
	} {
		got, err := NextRun(c.expr, c.tz, at(c.after))
		if err != nil || !got.Equal(at(c.want)) {
			t.Errorf("%s in %s after %s: %v %v, want %s", c.expr, c.tz, c.after, got, err, c.want)
		}
	}
	for _, bad := range [][2]string{{"@every 1m", "UTC"}, {"CRON_TZ=UTC 0 9 * * *", "UTC"}, {"TZ=UTC 0 9 * * *", "UTC"},
		{"61 * * * *", "UTC"}, {"0 9 * * *", "Mars/Olympus"}, {"0 9 * * *", ""}, {"0 9 * * *", "Local"}, {"0 0 30 2 *", "UTC"}, {"", "UTC"}} {
		if _, err := NextRun(bad[0], bad[1], time.Now()); err == nil {
			t.Errorf("%q in %q accepted", bad[0], bad[1])
		}
	}
}

func (f *fix) schedule(t *testing.T, ap uuid.UUID, expr string, next time.Time, by uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.exec(t, `INSERT INTO autoresearch_trigger (id, workspace_id, autoresearch_id, kind, cron, tz, next_run_at, created_by)
 VALUES ($1, $2, $3, 'schedule', $4, 'UTC', $5, $6)`, id, f.ws, ap, expr, next, by)
	return id
}

func (f *fix) runs(t *testing.T, ap uuid.UUID) []db.AutoresearchRun {
	t.Helper()
	rows, err := f.env.Owner.Query(context.Background(), `SELECT id FROM autoresearch_run WHERE autoresearch_id = $1 ORDER BY created_at`, ap)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []db.AutoresearchRun
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, f.run(t, id))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *fix) nextRun(t *testing.T, trigger uuid.UUID) time.Time {
	t.Helper()
	var next time.Time
	if err := f.env.Owner.QueryRow(context.Background(), `SELECT next_run_at FROM autoresearch_trigger WHERE id = $1`, trigger).Scan(&next); err != nil {
		t.Fatal(err)
	}
	return next
}

func TestAScheduleFiresOnTimeAndALateSlotIsSkipped(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	tr := f.schedule(t, ap, "0 * * * *", at("2026-10-13T06:00:00Z"), f.owner)

	f.now = at("2026-10-13T05:59:00Z")
	tick(t, f.s, ctx)
	if len(f.runs(t, ap)) != 0 {
		t.Fatal("fired early")
	}
	f.now = at("2026-10-13T06:04:00Z")
	tick(t, f.s, ctx)
	rs := f.runs(t, ap)
	if len(rs) != 1 || rs[0].Status != "running" || !rs[0].PlannedAt.Equal(at("2026-10-13T06:00:00Z")) || rs[0].Source != "schedule" ||
		rs[0].IdempotencyKey != "schedule:"+tr.String()+":2026-10-13T06:00:00Z" || !f.nextRun(t, tr).Equal(at("2026-10-13T07:00:00Z")) {
		t.Fatalf("on time: %+v next %v", rs, f.nextRun(t, tr))
	}

	f.now = at("2026-10-13T07:06:00Z")
	tick(t, f.s, ctx)
	rs = f.runs(t, ap)
	if len(rs) != 2 || reason(rs[1]) != "skipped/late" || !rs[1].PlannedAt.Equal(at("2026-10-13T07:00:00Z")) ||
		!f.nextRun(t, tr).Equal(at("2026-10-13T08:00:00Z")) {
		t.Fatalf("late: %+v", rs)
	}
}

func TestAfterDowntimeOnlyTheLatestMissedSlotFires(t *testing.T) {
	f := setup(t)
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	tr := f.schedule(t, ap, "0 * * * *", at("2026-10-13T01:00:00Z"), f.owner)
	f.now = at("2026-10-13T06:02:00Z")
	tick(t, f.s, context.Background())
	rs := f.runs(t, ap)
	if len(rs) != 1 || rs[0].Status != "running" || !rs[0].PlannedAt.Equal(at("2026-10-13T06:00:00Z")) ||
		!f.nextRun(t, tr).Equal(at("2026-10-13T07:00:00Z")) {
		t.Fatalf("%+v", rs)
	}
}

func TestOneSlotFiresOnceHoweverOftenItIsEvaluated(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	tr := f.schedule(t, ap, "0 * * * *", at("2026-10-13T06:00:00Z"), f.owner)
	f.now = at("2026-10-13T06:01:00Z")
	services := []*Service{f.s, {Store: f.s.Store, AgentRuns: f.s.AgentRuns, Now: f.s.Now}}
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ { // two services each tick twice concurrently
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			if err := s.Tick(ctx); err != nil {
				errs <- err
			}
		}(services[i%len(services)])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// a restored backup puts the same slot back: the key still holds
	f.exec(t, `UPDATE autoresearch_trigger SET next_run_at = $2 WHERE id = $1`, tr, at("2026-10-13T06:00:00Z"))
	tick(t, f.s, ctx)
	if rs := f.runs(t, ap); len(rs) != 1 || f.count(t, `SELECT count(*) FROM card`) != 1 {
		t.Fatalf("%d runs", len(rs))
	}
}

func TestPausedOrDisabledSchedulesDoNotFire(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	paused := f.autoresearch(t, "create_card", "Paused", "", "")
	f.exec(t, `UPDATE autoresearch SET status = 'paused' WHERE id = $1`, paused)
	pt := f.schedule(t, paused, "0 * * * *", at("2026-10-13T06:00:00Z"), f.owner)
	off := f.autoresearch(t, "create_card", "Off", "", "")
	ot := f.schedule(t, off, "0 * * * *", at("2026-10-13T06:00:00Z"), f.owner)
	f.exec(t, `UPDATE autoresearch_trigger SET enabled = false WHERE id = $1`, ot)
	f.now = at("2026-10-13T06:01:00Z")
	tick(t, f.s, ctx)
	if f.count(t, `SELECT count(*) FROM autoresearch_run`) != 0 || !f.nextRun(t, pt).Equal(at("2026-10-13T06:00:00Z")) {
		t.Fatal("a paused or disabled schedule was evaluated")
	}
}

func TestAScheduleOfAnActorWhoLeftKeepsFailingClosed(t *testing.T) {
	f := setup(t)
	gone := f.user(t, "gone@lab.org", "member")
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	tr := f.schedule(t, ap, "0 * * * *", at("2026-10-13T06:00:00Z"), gone)
	f.exec(t, `DELETE FROM member WHERE user_id = $1`, gone)
	f.now = at("2026-10-13T06:01:00Z")
	tick(t, f.s, context.Background())
	if rs := f.runs(t, ap); len(rs) != 1 || reason(rs[0]) != "failed/actor_revoked" || !f.nextRun(t, tr).Equal(at("2026-10-13T07:00:00Z")) {
		t.Fatalf("%+v", rs)
	}
}

func TestABrokenCronDisablesItsTrigger(t *testing.T) {
	f := setup(t)
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	tr := f.schedule(t, ap, "not a cron", at("2026-10-13T06:00:00Z"), f.owner)
	f.now = at("2026-10-13T06:01:00Z")
	tick(t, f.s, context.Background())
	if f.count(t, `SELECT count(*) FROM autoresearch_trigger WHERE id = $1 AND NOT enabled`, tr) != 1 ||
		f.count(t, `SELECT count(*) FROM event WHERE type = 'autoresearch_trigger.disabled'`) != 1 {
		t.Fatal("the broken trigger is still enabled")
	}
}

func TestTheTickRepairsARunACrashLeftQueued(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.s.Now = nil // the repair compares with the database's clock
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	ins := `INSERT INTO autoresearch_run (id, workspace_id, autoresearch_id, source, acting_user_id, planned_at, idempotency_key, created_at)
 VALUES ($1, $2, $3, 'manual', $4, now(), $5, now() - $6::interval)`
	stale, fresh := uuid.New(), uuid.New()
	f.exec(t, ins, stale, f.ws, ap, f.owner, "crash-1", "2 minutes")
	f.exec(t, ins, fresh, f.ws, ap, f.owner, "crash-2", "10 seconds")
	tick(t, f.s, ctx)
	if r := f.run(t, stale); r.Status != "running" || r.CardID == nil || r.IdempotencyKey != "crash-1" {
		t.Fatalf("stale run: %s", reason(r))
	}
	if r := f.run(t, fresh); r.Status != "queued" {
		t.Fatalf("a run younger than a minute was repaired: %s", reason(r))
	}
}

func tick(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleExactlyAtMaxLatenessStillFires(t *testing.T) {
	f := setup(t)
	ap := f.autoresearch(t, "create_card", "Boundary", "", "")
	f.schedule(t, ap, "0 * * * *", at("2026-10-13T06:00:00Z"), f.owner)
	f.now = at("2026-10-13T06:05:00Z")
	tick(t, f.s, context.Background())
	rs := f.runs(t, ap)
	if len(rs) != 1 || rs[0].Status != "running" {
		t.Fatalf("boundary: %+v", rs)
	}
}
func TestImpossibleCronDisablesItsTrigger(t *testing.T) {
	f := setup(t)
	ap := f.autoresearch(t, "create_card", "Impossible", "", "")
	tr := f.schedule(t, ap, "0 0 30 2 *", at("2026-10-13T06:00:00Z"), f.owner)
	f.now = at("2026-10-13T06:01:00Z")
	tick(t, f.s, context.Background())
	if f.count(t, `SELECT count(*) FROM autoresearch_trigger WHERE id = $1 AND NOT enabled`, tr) != 1 || f.count(t, `SELECT count(*) FROM event WHERE type = 'autoresearch_trigger.disabled'`) != 1 || len(f.runs(t, ap)) != 0 {
		t.Fatal("impossible cron was not disabled without firing")
	}
}
