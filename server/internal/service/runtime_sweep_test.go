package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func staleRuntime(t *testing.T, f *fixture, id uuid.UUID) {
	t.Helper()
	if _, err := f.Env.Owner.Exec(t.Context(), "UPDATE runtime SET last_seen_at=now()-interval '5 minutes' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
}
func runtimeStatus(t *testing.T, f *fixture, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := f.Env.Owner.QueryRow(t.Context(), "SELECT status FROM runtime WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestRuntimeSweepPublishesOnlyMetadataFreeCommittedChanges(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	stale1 := f.runtime(t, f.Owner, "stale-one", "claude")
	stale2 := f.runtime(t, f.Owner, "stale-two", "codex")
	fresh := f.runtime(t, f.Owner, "fresh", "claude")
	staleRuntime(t, f, stale1.RuntimeID)
	staleRuntime(t, f, stale2.RuntimeID)
	var published []db.Event
	f.T.Store.Publish = func(_ context.Context, events []db.Event) {
		for _, e := range events {
			if e.Type == "runtime.updated" {
				if runtimeStatus(t, f, stale1.RuntimeID) != "offline" || runtimeStatus(t, f, stale2.RuntimeID) != "offline" {
					t.Error("published before offline commit")
				}
				published = append(published, e)
			}
		}
	}
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 {
		t.Fatalf("runtime refreshes=%d want1", len(published))
	}
	e := published[0]
	var payload map[string]any
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if e.WorkspaceID != f.WS || e.ActorType != "system" || e.ActorID != nil || len(payload) != 1 || payload["action"] != "stale_sweep" {
		t.Fatalf("unexpected metadata: %+v payload=%v", e, payload)
	}
	if runtimeStatus(t, f, fresh.RuntimeID) != "online" {
		t.Fatal("fresh runtime expired")
	}
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 {
		t.Fatal("no-op sweep published another refresh")
	}
	// Existing runtime events are workspace-wide, with no extra expiry metadata.
	member, err := f.T.Store.Global().UpsertUserByEmail(ctx, "member-sweep@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: member.ID, Role: "member"})
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{f.Owner, member.ID} {
		if err = f.T.Store.InWorkspace(ctx, f.WS, id, func(tx *store.Tx) error {
			events, err := tx.Q.EventsAfter(ctx, db.EventsAfterParams{WorkspaceID: f.WS, After: e.ID - 1, Lim: 1})
			if err != nil {
				return err
			}
			if len(events) != 1 || events[0].ID != e.ID {
				t.Error("existing workspace event audience changed")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.T.Store.InWorkspace(ctx, uuid.New(), f.Owner, func(tx *store.Tx) error {
		events, err := tx.Q.EventsAfter(ctx, db.EventsAfterParams{WorkspaceID: f.WS, After: e.ID - 1, Lim: 1})
		if len(events) != 0 {
			t.Error("foreign workspace read expiry event")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSweepEventFailureRollsBackOfflineStatus(t *testing.T) {
	f := setup(t)
	ref := f.runtime(t, f.Owner, "rollback", "claude")
	staleRuntime(t, f, ref.RuntimeID)
	if _, err := f.Env.Owner.Exec(t.Context(), `CREATE FUNCTION reject_runtime_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='runtime.updated' THEN RAISE EXCEPTION 'injected refresh failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_runtime_refresh BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION reject_runtime_refresh()`); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Sweep(t.Context()); err == nil {
		t.Fatal("sweep ignored event failure")
	}
	if runtimeStatus(t, f, ref.RuntimeID) != "online" {
		t.Fatal("offline status survived event rollback")
	}
}

func TestRuntimeSweepRechecksHeartbeatAfterWorkspaceLock(t *testing.T) {
	f := setup(t)
	ref := f.runtime(t, f.Owner, "heartbeat-race", "claude")
	staleRuntime(t, f, ref.RuntimeID)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	heartbeat, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer heartbeat.Rollback(context.Background())
	if _, err = heartbeat.Exec(ctx, "SELECT id FROM workspace WHERE id=$1 FOR NO KEY UPDATE", f.WS); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	received := false
	go func() { done <- f.T.Sweep(ctx) }()
	defer func() {
		heartbeat.Rollback(context.Background())
		if !received {
			<-done
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err = f.Env.Owner.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) AND query LIKE '%LockWorkspaceExecution%')", heartbeat.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			received = true
			t.Fatalf("sweep finished without waiting on heartbeat workspace lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if _, err = heartbeat.Exec(ctx, "UPDATE runtime SET status='online',last_seen_at=now() WHERE id=$1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if err = heartbeat.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-done
	received = true
	if err != nil {
		t.Fatal(err)
	}
	if runtimeStatus(t, f, ref.RuntimeID) != "online" {
		t.Fatal("sweep overwrote committed heartbeat")
	}
	var count int
	if err = f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='runtime.updated'", f.WS).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("heartbeat no-op emitted an expiry refresh")
	}
}

func TestRuntimeSweepNewWorkspaceCandidateIsRecoveredNextPass(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	first := f.runtime(t, f.Owner, "initial-stale", "claude")
	staleRuntime(t, f, first.RuntimeID)
	other := &fixture{T: f.T, Env: f.Env, Owner: f.Owner, WS: uuid.MustParse("00000000-0000-0000-0000-000000000001")}
	err := f.T.Store.InWorkspace(ctx, other.WS, other.Owner, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: other.WS, Slug: "later-candidate", Name: "Later candidate"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: other.WS, UserID: other.Owner, Role: "owner"}); err != nil {
			return err
		}
		card, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: "grounding", WorkspaceID: other.WS, Number: 1, Title: "Later", Question: "Later", OwnerID: other.Owner})
		other.Card = card.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	other.enqueue(t, 1, "claude")
	ref, run := other.claimStart(t, other.runtime(t, other.Owner, "later-runtime", "claude"))
	if _, err = f.Env.Owner.Exec(ctx, "UPDATE agent_run SET max_attempts=1 WHERE id=$1", run); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, "SELECT id FROM workspace WHERE id=$1 FOR NO KEY UPDATE", f.WS); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	received := false
	go func() { done <- f.T.Sweep(ctx) }()
	defer func() {
		blocker.Rollback(context.Background())
		if !received {
			<-done
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err = f.Env.Owner.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) AND query LIKE '%LockWorkspaceExecution%')", blocker.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			received = true
			t.Fatalf("sweep missed discovery blocker: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	// Deletion creates a newly eligible attempt after the discovery snapshot.
	if _, err = f.Env.Owner.Exec(ctx, "DELETE FROM runtime WHERE id=$1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	laterLock, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer laterLock.Rollback(context.Background())
	if _, err = laterLock.Exec(ctx, "SELECT id FROM workspace WHERE id=$1 FOR NO KEY UPDATE", other.WS); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// The first pass must not extend its lock set backwards into the new workspace.
	err = <-done
	received = true
	if err != nil {
		t.Fatal(err)
	}
	var status string
	if err = f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", run).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" || f.settledCount() != 0 {
		t.Fatalf("new candidate handled outside locked batch: %s", status)
	}
	if err = laterLock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", run).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || f.settledCount() != 1 {
		t.Fatalf("candidate lost after next pass: %s settlements=%d", status, f.settledCount())
	}
}
