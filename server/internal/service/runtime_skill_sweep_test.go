package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func seedSkillSweepRequest(t *testing.T, f *fixture, runtime uuid.UUID, daemon, status string, expiry time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.Env.Owner.Exec(t.Context(), `INSERT INTO runtime_skill_request
		(workspace_id,id,runtime_id,owner_id,daemon_id,operation,status,expires_at,acquisition_id,acquired_at,completed_at)
		VALUES ($1,$2,$3,$4,$5,'list',$6,$7,
		CASE WHEN $6='running' THEN gen_random_uuid() ELSE NULL END,
		CASE WHEN $6='running' THEN now() ELSE NULL END,
		CASE WHEN $6 IN ('completed','failed','timeout') THEN now() ELSE NULL END)`,
		f.WS, id, runtime, f.Owner, daemon, status, expiry)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRuntimeSkillSweepSharesBatchAcrossWorkspaces(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	first := f.runtime(t, f.Owner, "busy-skill", "claude")
	for i := 0; i < 64; i++ {
		seedSkillSweepRequest(t, f, first.RuntimeID, "busy-skill", "pending", time.Now().Add(-2*time.Minute))
	}
	other := &fixture{T: f.T, Env: f.Env, Owner: f.Owner, WS: uuid.New()}
	if err := f.T.Store.InWorkspace(t.Context(), other.WS, other.Owner, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(t.Context(), db.CreateWorkspaceParams{ID: other.WS, Slug: "s" + other.WS.String()[:8], Name: "other"}); err != nil {
			return err
		}
		return tx.Q.AddMember(t.Context(), db.AddMemberParams{WorkspaceID: other.WS, UserID: other.Owner, Role: "owner"})
	}); err != nil {
		t.Fatal(err)
	}
	second := other.runtime(t, other.Owner, "other-skill", "claude")
	id := seedSkillSweepRequest(t, other, second.RuntimeID, "other-skill", "pending", time.Now().Add(-time.Minute))
	if err := sweep.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := skillSweepStatus(t, other, id); got != "timeout" {
		t.Fatalf("other workspace request: %s", got)
	}
}

func TestRuntimeSkillSweepIsolatesPoisonWorkspace(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	first := f.runtime(t, f.Owner, "poison-skill", "claude")
	if _, err := f.Env.Owner.Exec(t.Context(), `INSERT INTO runtime_skill_request
		(workspace_id,runtime_id,owner_id,daemon_id,operation,expires_at)
		SELECT $1,$2,$3,'poison-skill','list',now()-interval '2 minutes' FROM generate_series(1,4097)`, f.WS, first.RuntimeID, f.Owner); err != nil {
		t.Fatal(err)
	}
	other := &fixture{T: f.T, Env: f.Env, Owner: f.Owner, WS: uuid.New()}
	if err := f.T.Store.InWorkspace(t.Context(), other.WS, other.Owner, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(t.Context(), db.CreateWorkspaceParams{ID: other.WS, Slug: "s" + other.WS.String()[:8], Name: "other"}); err != nil {
			return err
		}
		return tx.Q.AddMember(t.Context(), db.AddMemberParams{WorkspaceID: other.WS, UserID: other.Owner, Role: "owner"})
	}); err != nil {
		t.Fatal(err)
	}
	second := other.runtime(t, other.Owner, "healthy-skill", "claude")
	id := seedSkillSweepRequest(t, other, second.RuntimeID, "healthy-skill", "pending", time.Now().Add(-time.Minute))
	query := fmt.Sprintf(`CREATE FUNCTION reject_poison_skill_timeout() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.workspace_id='%s'::uuid AND NEW.type='runtime_skill_request.updated' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_poison_skill_timeout BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION reject_poison_skill_timeout()`, f.WS)
	if _, err := f.Env.Owner.Exec(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	if err := sweep.Sweep(t.Context()); err == nil {
		t.Fatal("expected poison workspace error")
	}
	if got := skillSweepStatus(t, other, id); got != "timeout" {
		t.Fatalf("healthy workspace was starved: %s", got)
	}
	var changed int
	if err := f.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM runtime_skill_request WHERE workspace_id=$1 AND status='timeout'`, f.WS).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Fatalf("poison workspace did not roll back: %d", changed)
	}
}

func skillSweepStatus(t *testing.T, f *fixture, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := f.Env.Owner.QueryRow(t.Context(), `SELECT status FROM runtime_skill_request WHERE workspace_id=$1 AND id=$2`, f.WS, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func seedSkillSweepWorkspace(t *testing.T, f *fixture, ws uuid.UUID, name string) (*fixture, uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	runtimeID, requestID := uuid.New(), uuid.New()
	tx, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `INSERT INTO workspace(id,slug,name) VALUES ($1,$2,$2)`, ws, name); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, f.Owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO runtime(id,workspace_id,owner_id,daemon_id,harness) VALUES ($1,$2,$3,'sweep','claude')`, runtimeID, ws, f.Owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO runtime_skill_request(workspace_id,id,runtime_id,owner_id,daemon_id,operation,expires_at) VALUES ($1,$2,$3,$4,'sweep','list',now()-interval '2 minutes')`, ws, requestID, runtimeID, f.Owner); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return &fixture{T: f.T, Env: f.Env, Owner: f.Owner, WS: ws}, requestID
}

func TestRuntimeSkillSweepRotatesPastManyPoisonWorkspaces(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	for i := 1; i <= 256; i++ {
		ws := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012x", i))
		seedSkillSweepWorkspace(t, f, ws, fmt.Sprintf("poison-%d", i))
	}
	healthyWS := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	healthy, id := seedSkillSweepWorkspace(t, f, healthyWS, "healthy")
	query := fmt.Sprintf(`CREATE FUNCTION reject_many_poison_timeouts() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.workspace_id<>'%s'::uuid AND NEW.type='runtime_skill_request.updated' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_many_poison_timeouts BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION reject_many_poison_timeouts()`, healthyWS)
	if _, err := f.Env.Owner.Exec(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	if err := sweep.Sweep(t.Context()); err == nil {
		t.Fatal("expected errors for poison workspaces")
	}
	if got := skillSweepStatus(t, healthy, id); got != "pending" {
		t.Fatalf("expected first pass to respect workspace budget: %s", got)
	}
	for i := 0; i < 3 && skillSweepStatus(t, healthy, id) != "timeout"; i++ {
		_ = sweep.Sweep(t.Context())
	}
	if got := skillSweepStatus(t, healthy, id); got != "timeout" {
		t.Fatalf("healthy workspace starved across sweeps: %s", got)
	}
}

func TestRuntimeSkillSweepSkipsHeldWorkspaceLock(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	blockedWS := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	blocked, blockedID := seedSkillSweepWorkspace(t, f, blockedWS, "blocked")
	healthyWS := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	healthy, healthyID := seedSkillSweepWorkspace(t, f, healthyWS, "healthy")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	blocker, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT id FROM workspace WHERE id=$1 FOR NO KEY UPDATE`, blockedWS); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sweep.Sweep(ctx) }()
	for {
		var waiting bool
		if err := f.Env.Owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) AND query LIKE '%LockWorkspaceExecution%')`, blocker.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("sweep did not attempt blocked workspace: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	started := time.Now()
	if err := sweep.Sweep(ctx); err != nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("overlapping sweep blocked shared loop: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected held workspace timeout")
		}
	case <-ctx.Done():
		t.Fatal("sweep failed to continue past held workspace")
	}
	if got := skillSweepStatus(t, blocked, blockedID); got != "pending" {
		t.Fatalf("blocked workspace changed: %s", got)
	}
	if got := skillSweepStatus(t, healthy, healthyID); got != "timeout" {
		t.Fatalf("healthy workspace starved by held lock: %s", got)
	}
}

func TestRuntimeSkillSweepBoundsTotalPassDuration(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	var workspaces []uuid.UUID
	for i := 1; i <= 12; i++ {
		ws := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012x", i))
		seedSkillSweepWorkspace(t, f, ws, fmt.Sprintf("held-%d", i))
		workspaces = append(workspaces, ws)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	blocker, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT id FROM workspace WHERE id=ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE`, workspaces); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = sweep.Sweep(ctx)
	if err == nil || time.Since(started) > 15*time.Second {
		t.Fatalf("sweep failed to stop at its pass deadline after %v: %v", time.Since(started), err)
	}
	var waiting int
	if err := f.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid))`, blocker.Conn().PgConn().PID()).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if waiting != 0 {
		t.Fatalf("%d test transactions still waiting after sweep returned", waiting)
	}
}

func TestRuntimeSkillSweepExpiresAndPublishesCommittedEvents(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	rt := f.runtime(t, f.Owner, "skill-sweep", "claude")
	expiredPending := seedSkillSweepRequest(t, f, rt.RuntimeID, "skill-sweep", "pending", time.Now().Add(-2*time.Minute))
	expiredRunning := seedSkillSweepRequest(t, f, rt.RuntimeID, "skill-sweep", "running", time.Now().Add(-time.Minute))
	future := seedSkillSweepRequest(t, f, rt.RuntimeID, "skill-sweep", "pending", time.Now().Add(time.Hour))
	completed := seedSkillSweepRequest(t, f, rt.RuntimeID, "skill-sweep", "completed", time.Now().Add(-time.Hour))
	var published []db.Event
	f.T.Store.Publish = func(_ context.Context, events []db.Event) {
		for _, event := range events {
			if event.Type == "runtime_skill_request.updated" {
				if skillSweepStatus(t, f, expiredPending) != "timeout" || skillSweepStatus(t, f, expiredRunning) != "timeout" {
					t.Error("published before timeout transaction committed")
				}
				published = append(published, event)
			}
		}
	}
	if err := sweep.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 {
		t.Fatalf("published %d events, want 2", len(published))
	}
	for _, event := range published {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if event.WorkspaceID != f.WS || event.ActorType != "user" || event.ActorID == nil || *event.ActorID != f.Owner || event.RecipientID != nil || len(payload) != 4 || payload["status"] != "timeout" || payload["operation"] != "list" || payload["runtime_id"] != rt.RuntimeID.String() {
			t.Fatalf("unexpected timeout event: %+v %v", event, payload)
		}
	}
	if skillSweepStatus(t, f, future) != "pending" || skillSweepStatus(t, f, completed) != "completed" {
		t.Fatal("changed a non-expired or completed request")
	}
	member, err := f.T.Store.Global().UpsertUserByEmail(t.Context(), "skill-sweep-member@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.T.Store.InWorkspace(t.Context(), f.WS, f.Owner, func(tx *store.Tx) error {
		return tx.Q.AddMember(t.Context(), db.AddMemberParams{WorkspaceID: f.WS, UserID: member.ID, Role: "member"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Store.InWorkspace(t.Context(), f.WS, member.ID, func(tx *store.Tx) error {
		events, err := tx.Q.EventsAfter(t.Context(), db.EventsAfterParams{WorkspaceID: f.WS, After: published[0].ID - 1, Lim: 10})
		if err == nil && (len(events) < 2 || events[0].ID != published[0].ID || events[1].ID != published[1].ID) {
			t.Error("workspace member cannot see existing workspace-visible timeout events")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Store.InWorkspace(t.Context(), uuid.New(), f.Owner, func(tx *store.Tx) error {
		events, err := tx.Q.EventsAfter(t.Context(), db.EventsAfterParams{WorkspaceID: f.WS, After: published[0].ID - 1, Lim: 10})
		if err == nil && len(events) != 0 {
			t.Error("foreign workspace could see timeout events")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := sweep.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 {
		t.Fatal("a repeated sweep published duplicate events")
	}
}

func TestRuntimeSkillSweepDiscoveryIgnoresTempTableShadow(t *testing.T) {
	f := setup(t)
	rt := f.runtime(t, f.Owner, "shadow-skill", "claude")
	seedSkillSweepRequest(t, f, rt.RuntimeID, "shadow-skill", "pending", time.Now().Add(-time.Minute))
	ctx := t.Context()
	tx, err := f.Env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$1,true)`, uuid.Nil.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE runtime_skill_request (workspace_id uuid,id uuid,status text,expires_at timestamptz); INSERT INTO runtime_skill_request VALUES (gen_random_uuid(),gen_random_uuid(),'pending',now()-interval '1 hour'); SET LOCAL search_path=pg_temp,public`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT * FROM public.runtime_skill_timeout_candidates($1)`, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var eligible bool
		if err := rows.Scan(&id, &eligible); err != nil {
			t.Fatal(err)
		}
		if eligible {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != f.WS {
		t.Fatalf("temp table affected candidate discovery: %v", ids)
	}
}

func TestRuntimeSkillSweepBoundedAndOldestFirst(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	rt := f.runtime(t, f.Owner, "bounded-skill", "claude")
	ids := make([]uuid.UUID, 65)
	for i := range ids {
		ids[i] = seedSkillSweepRequest(t, f, rt.RuntimeID, "bounded-skill", "pending", time.Now().Add(time.Duration(i-100)*time.Second))
	}
	if err := sweep.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		want := "timeout"
		if i == len(ids)-1 {
			want = "pending"
		}
		if got := skillSweepStatus(t, f, id); got != want {
			t.Fatalf("request %d: got %s, want %s", i, got, want)
		}
	}
	if err := sweep.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := skillSweepStatus(t, f, ids[64]); got != "timeout" {
		t.Fatalf("remaining request: %s", got)
	}
}

func TestRuntimeSkillSweepRollsBackIfEventFails(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	rt := f.runtime(t, f.Owner, "failure-skill", "claude")
	id := seedSkillSweepRequest(t, f, rt.RuntimeID, "failure-skill", "pending", time.Now().Add(-time.Minute))
	if _, err := f.Env.Owner.Exec(t.Context(), `CREATE FUNCTION reject_skill_timeout() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='runtime_skill_request.updated' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_skill_timeout BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION reject_skill_timeout()`); err != nil {
		t.Fatal(err)
	}
	if err := sweep.Sweep(t.Context()); err == nil {
		t.Fatal("sweep ignored event failure")
	}
	if got := skillSweepStatus(t, f, id); got != "pending" {
		t.Fatalf("timeout survived event rollback: %s", got)
	}
}

func TestRuntimeSkillSweepCandidateDiscoveryRequiresMaintenanceContext(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	tx, err := f.Env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true), set_config('app.user_id',$2,true)`, f.WS.String(), f.Owner.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `SELECT * FROM public.runtime_skill_timeout_candidates($1)`, uuid.Nil)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("ordinary context discovery: %v", err)
	}
}

func TestRuntimeSkillSweepRechecksFinishedRequestAfterLock(t *testing.T) {
	f := setup(t)
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	rt := f.runtime(t, f.Owner, "race-skill", "claude")
	id := seedSkillSweepRequest(t, f, rt.RuntimeID, "race-skill", "running", time.Now().Add(-time.Minute))
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	blocker, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `SELECT id FROM workspace WHERE id=$1 FOR NO KEY UPDATE`, f.WS); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sweep.Sweep(ctx) }()
	defer func() {
		_ = blocker.Rollback(context.Background())
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
	for {
		var blocked bool
		err = f.Env.Owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) AND query LIKE '%LockWorkspaceExecution%')`, blocker.Conn().PgConn().PID()).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("sweep did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = blocker.Exec(ctx, `UPDATE runtime_skill_request SET status='completed', completed_at=now() WHERE workspace_id=$1 AND id=$2`, f.WS, id); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if got := skillSweepStatus(t, f, id); got != "completed" {
		t.Fatalf("finished request overwritten: %s", got)
	}
}

func TestRuntimeSkillSweepPagesPastEmptyWorkspaces(t *testing.T) {
	f := setup(t)
	for i := 1; i <= 270; i++ {
		ws := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012x", i))
		_, err := f.Env.Owner.Exec(t.Context(), `INSERT INTO workspace(id,slug,name) VALUES ($1,$2,$2)`, ws, fmt.Sprintf("empty-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	healthy, id := seedSkillSweepWorkspace(t, f, uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"), "after-empty")
	sweep := NewRuntimeSkillSweeper(f.T.Store)
	more, err := sweep.SweepMore(t.Context())
	if err != nil || !more {
		t.Fatalf("expected continuation: more=%v err=%v", more, err)
	}
	if got := skillSweepStatus(t, healthy, id); got != "pending" {
		t.Fatalf("first page should leave healthy pending: %s", got)
	}
	if err := sweep.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := skillSweepStatus(t, healthy, id); got != "timeout" {
		t.Fatalf("empty workspaces blocked cursor: %s", got)
	}
}

func TestRuntimeSkillSweepConcurrentReplicasPublishOnce(t *testing.T) {
	f := setup(t)
	rt := f.runtime(t, f.Owner, "concurrent-skill", "claude")
	id := seedSkillSweepRequest(t, f, rt.RuntimeID, "concurrent-skill", "pending", time.Now().Add(-time.Minute))
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; results <- NewRuntimeSkillSweeper(f.T.Store).Sweep(t.Context()) }()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := skillSweepStatus(t, f, id); got != "timeout" {
		t.Fatalf("status: %s", got)
	}
	var n int
	if err := f.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='runtime_skill_request.updated' AND payload->>'request_id'=$2`, f.WS, id.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected one timeout event, got %d", n)
	}
}

func TestRuntimeSkillSweepEmptyNeighborsDoNotReduceBatch(t *testing.T) {
	f := setup(t)
	busy, id := seedSkillSweepWorkspace(t, f, uuid.MustParse("00000000-0000-0000-0000-000000000001"), "busy-full-batch")
	for i := 2; i <= 64; i++ {
		ws := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012x", i))
		if _, err := f.Env.Owner.Exec(t.Context(), `INSERT INTO workspace(id,slug,name) VALUES ($1,$2,$2)`, ws, fmt.Sprintf("neighbor-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Env.Owner.Exec(t.Context(), `INSERT INTO runtime_skill_request(workspace_id,runtime_id,owner_id,daemon_id,operation,expires_at) SELECT workspace_id,runtime_id,owner_id,daemon_id,operation,expires_at FROM runtime_skill_request,generate_series(1,63) WHERE workspace_id=$1 AND id=$2`, busy.WS, id); err != nil {
		t.Fatal(err)
	}
	if err := NewRuntimeSkillSweeper(f.T.Store).Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM runtime_skill_request WHERE workspace_id=$1 AND status='timeout'`, busy.WS).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 64 {
		t.Fatalf("expected 64 timeouts, got %d", n)
	}
}
