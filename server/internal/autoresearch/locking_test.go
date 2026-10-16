package autoresearch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestDispatchTakesExecutionLockBeforeEvents(t *testing.T) {
	f := setup(t)
	ap := f.autoresearch(t, "run_only", "Digest", "Summarise the week.", "")
	f.online(t, f.owner, "claude")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var run db.AutoresearchRun
	if err := f.s.Store.InWorkspace(ctx, f.ws, f.owner, func(tx *store.Tx) error {
		var err error
		run, _, err = f.s.Admit(ctx, tx, Admission{Autoresearch: ap, Actor: f.owner, Source: "manual", Key: uuid.NewString(), PlannedAt: f.now})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := db.New(blocker).LockWorkspaceExecution(ctx, f.ws); err != nil {
		t.Fatal(err)
	}
	var pid uint32
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.s.Dispatch(ctx, f.ws, run.ID) }()
	// Observe the real dispatch transaction waiting on this workspace row before
	// requesting the event lock as a daemon claim would.
	var waiting int
	var query, event string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err = f.env.Owner.QueryRow(ctx, `SELECT pid, query, wait_event FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, pid).Scan(&waiting, &query, &event)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting == 0 {
		t.Fatalf("dispatch did not wait for workspace lock: %v", err)
	}
	var advisory int
	if err := f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE pid = $1 AND locktype = 'advisory' AND granted`, waiting).Scan(&advisory); err != nil {
		t.Fatal(err)
	}
	t.Logf("blocker pid=%d dispatch pid=%d wait_event=%s granted_advisory=%d query=%s", pid, waiting, event, advisory, query)
	eventErr := db.New(blocker).LockWorkspaceEvents(ctx, f.ws)
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	dispatchErr := <-done
	if advisory != 0 || eventErr != nil || dispatchErr != nil {
		t.Fatalf("workspace/event inversion: granted_advisory=%d event=%v dispatch=%v", advisory, eventErr, dispatchErr)
	}
	if r := f.run(t, run.ID); r.Status != "running" || r.AgentRunID == nil {
		t.Fatalf("dispatch: %+v", r)
	}
}
