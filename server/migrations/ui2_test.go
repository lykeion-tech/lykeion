package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

var ui2Tables = []string{"workspace_skill", "workspace_skill_version", "workspace_skill_pin", "workspace_mcp_server", "runtime_skill_request"}

// A missing policy or grant must prevent this test's scoped reads and writes.
func TestUI2TenantTables(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	skill := seedUI2Skill(t, env, ws, u, "analysis")
	seedUI2Pin(t, env, ws, card, skill)
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace_mcp_server (workspace_id, name, transport, config_ciphertext, created_by) VALUES ($1, 'remote', 'url', $2, $3)`, ws, make([]byte, 29), u); err != nil {
		t.Fatal(err)
	}
	runtime := seedUI2Runtime(t, env, ws, u)
	if _, err := env.Owner.Exec(ctx, `INSERT INTO runtime_skill_request (workspace_id, runtime_id, owner_id, daemon_id, operation) VALUES ($1, $2, $3, 'daemon', 'list')`, ws, runtime, u); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=ANY($1) AND c.relrowsecurity AND EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid)`, ui2Tables).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("tenant tables with policies: %d, want 5", count)
	}
	for _, table := range ui2Tables {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id', $1, true)`, ws.String()); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s: owning workspace saw %d rows", table, count)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id', $1, true)`, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s: foreign workspace saw %d rows", table, count)
		}
		tx.Rollback(ctx)
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id', $1, true)`, ws.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_skill (workspace_id, name, created_by) VALUES ($1, 'foreign', $2)`, uuid.New(), u)
	wantCode(t, err, "42501", "foreign workspace write")
}

// Dropping any tenant component from a foreign key accepts one of these writes.
func TestUI2CrossWorkspaceReferencesFail(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	other := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id,slug,name) VALUES ($1,'other','other')`, other); err != nil {
		t.Fatal(err)
	}
	skill := seedUI2Skill(t, env, ws, u, "analysis")
	otherSkill := seedUI2Skill(t, env, other, u, "analysis")
	runtime := seedUI2Runtime(t, env, ws, u)
	otherUser := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id,email) VALUES ($1,'other@x')`, otherUser); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, sql string
		args      []any
	}{
		{"version", `INSERT INTO workspace_skill_version (workspace_id,skill_id,revision,name,description,sha256,manifest,blob_key) VALUES ($1,$2,2,'analysis','', $3,'{}',$4)`, []any{other, skill, strings.Repeat("a", 64), other.String() + "/skills/" + strings.Repeat("a", 64)}},
		{"pin version", `INSERT INTO workspace_skill_pin (workspace_id,scope_kind,scope_id,skill_id,revision) VALUES ($1,'card',$2,$3,1)`, []any{ws, card, otherSkill}},
		{"pin card", `INSERT INTO workspace_skill_pin (workspace_id,scope_kind,scope_id,skill_id,revision) VALUES ($1,'card',$2,$3,1)`, []any{other, card, otherSkill}},
		{"runtime", `INSERT INTO runtime_skill_request (workspace_id,runtime_id,owner_id,daemon_id,operation) VALUES ($1,$2,$3,'daemon','list')`, []any{other, runtime, u}},
		{"runtime owner", `INSERT INTO runtime_skill_request (workspace_id,runtime_id,owner_id,daemon_id,operation) VALUES ($1,$2,$3,'daemon','list')`, []any{ws, runtime, otherUser}},
		{"runtime daemon", `INSERT INTO runtime_skill_request (workspace_id,runtime_id,owner_id,daemon_id,operation) VALUES ($1,$2,$3,'other','list')`, []any{ws, runtime, u}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.Owner.Exec(ctx, tc.sql, tc.args...)
			wantCode(t, err, "23503", tc.name)
		})
	}
}

// Repointing a revision or cascading a soft-delete would change the pinned hash.
func TestSkillVersionAndPinSurviveSoftDelete(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	skill := seedUI2Skill(t, env, ws, u, "analysis")
	seedUI2Pin(t, env, ws, card, skill)
	_, err := env.Owner.Exec(ctx, `UPDATE workspace_skill_version SET description=$3 WHERE workspace_id=$1 AND skill_id=$2`, ws, skill, "changed description")
	wantCode(t, err, "23514", "immutable version")
	_, err = env.Owner.Exec(ctx, `INSERT INTO workspace_skill (workspace_id,name,created_by) VALUES ($1,'analysis',$2)`, ws, u)
	wantCode(t, err, "23505", "duplicate active name")
	if _, err := env.Owner.Exec(ctx, `UPDATE workspace_skill SET deleted_at=now() WHERE workspace_id=$1 AND id=$2`, ws, skill); err != nil {
		t.Fatal(err)
	}
	seedUI2Skill(t, env, ws, u, "analysis")
	var hash, name, key string
	if err := env.Owner.QueryRow(ctx, `SELECT v.sha256,v.name,v.blob_key FROM workspace_skill_pin p JOIN workspace_skill_version v USING (workspace_id,skill_id,revision) WHERE p.workspace_id=$1 AND p.scope_kind='card' AND p.scope_id=$2`, ws, card).Scan(&hash, &name, &key); err != nil {
		t.Fatal(err)
	}
	if hash != strings.Repeat("a", 64) || name != "analysis" || key != ws.String()+"/skills/"+strings.Repeat("a", 64) {
		t.Fatalf("pin changed: %q %q %q", hash, name, key)
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, ws.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM workspace_skill_version WHERE workspace_id=$1 AND skill_id=$2`, ws, skill)
	wantCode(t, err, "42501", "application version deletion")
}

func seedUI2Skill(t *testing.T, env *testutil.Env, ws, u uuid.UUID, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	tx, err := env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `INSERT INTO workspace_skill (workspace_id,name,created_by) VALUES ($1,$2,$3) RETURNING id`, ws, name, u).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workspace_skill_version (workspace_id,skill_id,revision,name,description,sha256,manifest,blob_key) VALUES ($1,$2,1,$3,'', $4,'{}',$5)`, ws, id, name, strings.Repeat("a", 64), ws.String()+"/skills/"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}
func seedUI2Pin(t *testing.T, env *testutil.Env, ws, card, skill uuid.UUID) {
	t.Helper()
	if _, err := env.Owner.Exec(context.Background(), `INSERT INTO workspace_skill_pin (workspace_id,scope_kind,scope_id,skill_id,revision) VALUES ($1,'card',$2,$3,1)`, ws, card, skill); err != nil {
		t.Fatal(err)
	}
}
func seedUI2Runtime(t *testing.T, env *testutil.Env, ws, u uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.Owner.QueryRow(context.Background(), `INSERT INTO runtime (workspace_id,owner_id,daemon_id,harness) VALUES ($1,$2,'daemon','claude') RETURNING id`, ws, u).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Every saved catalogue mutation must invalidate the previous revision.
func TestUI2ConnectorRevisionAdvances(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, _ := seedWorkspace(t, env)
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace_connector (workspace_id,name,updated_by) VALUES ($1,'ensembl',$2)`, ws, u); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := env.Owner.QueryRow(ctx, `UPDATE workspace_connector SET enabled=false WHERE workspace_id=$1 AND name='ensembl' RETURNING revision`, ws).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("saved catalogue revision: %d, want 1", revision)
	}
}

// Wrong authorities, stale acquisitions and expired leases must never publish results.
func TestUI2RuntimeRequestFences(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, _ := seedWorkspace(t, env)
	runtime := seedUI2Runtime(t, env, ws, u)
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, ws.String()); err != nil {
		t.Fatal(err)
	}
	q := db.New(tx)
	id, acquisition := uuid.New(), uuid.New()
	if _, err := q.InsertRuntimeSkillRequest(ctx, db.InsertRuntimeSkillRequestParams{WorkspaceID: ws, ID: id, RuntimeID: runtime, OwnerID: u, DaemonID: "daemon", Operation: "list", Keys: []string{}}); err != nil {
		t.Fatal(err)
	}
	claim := db.ClaimRuntimeSkillRequestParams{WorkspaceID: ws, ID: id, RuntimeID: runtime, OwnerID: u, DaemonID: "daemon", AcquisitionID: &acquisition}
	for _, mutate := range []func(*db.ClaimRuntimeSkillRequestParams){func(p *db.ClaimRuntimeSkillRequestParams) { p.WorkspaceID = uuid.New() }, func(p *db.ClaimRuntimeSkillRequestParams) { p.RuntimeID = uuid.New() }, func(p *db.ClaimRuntimeSkillRequestParams) { p.OwnerID = uuid.New() }, func(p *db.ClaimRuntimeSkillRequestParams) { p.DaemonID = "other" }} {
		bad := claim
		mutate(&bad)
		if _, err := q.ClaimRuntimeSkillRequest(ctx, bad); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("unauthorized acquisition: %v", err)
		}
	}
	row, err := q.ClaimRuntimeSkillRequest(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "running" || row.AcquisitionID == nil || *row.AcquisitionID != acquisition {
		t.Fatalf("acquired request: %+v", row)
	}
	if _, err := q.ClaimRuntimeSkillRequest(ctx, claim); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("double acquisition: %v", err)
	}
	finish := db.CompleteRuntimeSkillRequestParams{WorkspaceID: ws, ID: id, RuntimeID: runtime, OwnerID: u, DaemonID: "daemon", AcquisitionID: &acquisition, Status: "completed", Result: []byte(`{"items":[]}`)}
	for _, mutate := range []func(*db.CompleteRuntimeSkillRequestParams){func(p *db.CompleteRuntimeSkillRequestParams) { x := uuid.New(); p.AcquisitionID = &x }, func(p *db.CompleteRuntimeSkillRequestParams) { p.OwnerID = uuid.New() }, func(p *db.CompleteRuntimeSkillRequestParams) { p.DaemonID = "other" }, func(p *db.CompleteRuntimeSkillRequestParams) { p.Status = "pending" }} {
		bad := finish
		mutate(&bad)
		if _, err := q.CompleteRuntimeSkillRequest(ctx, bad); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("stale completion: %v", err)
		}
	}
	if _, err := q.CompleteRuntimeSkillRequest(ctx, finish); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CompleteRuntimeSkillRequest(ctx, finish); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("duplicate completion: %v", err)
	}
	expired := uuid.New()
	if _, err := q.InsertRuntimeSkillRequest(ctx, db.InsertRuntimeSkillRequestParams{WorkspaceID: ws, ID: expired, RuntimeID: runtime, OwnerID: u, DaemonID: "daemon", Operation: "list", Keys: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE runtime_skill_request SET expires_at=now()-interval '1 second' WHERE workspace_id=$1 AND id=$2`, ws, expired); err != nil {
		t.Fatal(err)
	}
	claim.ID = expired
	if _, err := q.ClaimRuntimeSkillRequest(ctx, claim); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired acquisition: %v", err)
	}
	timed, err := q.TimeoutRuntimeSkillRequests(ctx, db.TimeoutRuntimeSkillRequestsParams{WorkspaceID: ws, RuntimeID: runtime, OwnerID: u})
	if err != nil {
		t.Fatal(err)
	}
	if len(timed) != 1 || timed[0].ID != expired || timed[0].Status != "timeout" {
		t.Fatalf("timeouts: %+v", timed)
	}
}

// Both supported scope identities must retain their original revision.
func TestUI2AutoresearchPinsAndWorkspaceCleanup(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	skill := seedUI2Skill(t, env, ws, u, "analysis")
	var autoresearch, run uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO autoresearch (workspace_id,owner_id,title,execution_mode) VALUES ($1,$2,'a','run_only') RETURNING id`, ws, u).Scan(&autoresearch); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO autoresearch_run (workspace_id,autoresearch_id,source,acting_user_id,planned_at,idempotency_key) VALUES ($1,$2,'manual',$3,now(),'pin') RETURNING id`, ws, autoresearch, u).Scan(&run); err != nil {
		t.Fatal(err)
	}
	seedUI2Pin(t, env, ws, card, skill)
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace_skill_pin (workspace_id,scope_kind,scope_id,skill_id,revision) VALUES ($1,'autoresearch_run',$2,$3,1)`, ws, run, skill); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id,slug,name) VALUES ($1,'other','other')`, other); err != nil {
		t.Fatal(err)
	}
	otherSkill := seedUI2Skill(t, env, other, u, "analysis")
	_, err := env.Owner.Exec(ctx, `INSERT INTO workspace_skill_pin (workspace_id,scope_kind,scope_id,skill_id,revision) VALUES ($1,'autoresearch_run',$2,$3,1)`, other, run, otherSkill)
	wantCode(t, err, "23503", "cross-workspace Autoresearch scope")
	_, err = env.Owner.Exec(ctx, `UPDATE workspace_skill_pin SET revision=2 WHERE workspace_id=$1`, ws)
	wantCode(t, err, "23514", "immutable pin")
	if _, err := env.Owner.Exec(ctx, `UPDATE workspace_skill SET deleted_at=now() WHERE workspace_id=$1 AND id=$2`, ws, skill); err != nil {
		t.Fatal(err)
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, ws.String()); err != nil {
		t.Fatal(err)
	}
	q := db.New(tx)
	pinned, err := q.GetWorkspaceSkillPin(ctx, db.GetWorkspaceSkillPinParams{WorkspaceID: ws, ScopeKind: "autoresearch_run", ScopeID: run, SkillID: skill})
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Revision != 1 || pinned.Sha256 != strings.Repeat("a", 64) {
		t.Fatalf("Autoresearch pin changed: %+v", pinned)
	}
	active, err := q.ListWorkspaceSkills(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("deleted skill still listed: %+v", active)
	}
	tx.Rollback(ctx)
	if _, err := env.Owner.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, ws); err != nil {
		t.Fatalf("workspace cascade: %v", err)
	}
	var n int
	for _, table := range ui2Tables {
		if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE workspace_id=$1`, ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("workspace cascade retained %d %s rows", n, table)
		}
	}
}

func TestRuntimeExpiryDiscoveryIsIDOnlyAndMutationStaysScoped(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, _ := seedWorkspace(t, env)
	runtime := seedUI2Runtime(t, env, ws, u)
	if _, err := env.Owner.Exec(ctx, "UPDATE runtime SET last_seen_at=now()-interval '5 minutes' WHERE id=$1", runtime); err != nil {
		t.Fatal(err)
	}
	var publicGrant bool
	if err := env.Owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_proc p, LATERAL aclexplode(p.proacl) a WHERE p.oid='runtime_sweep_candidates(interval)'::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`).Scan(&publicGrant); err != nil {
		t.Fatal(err)
	}
	if publicGrant {
		t.Fatal("maintenance helper is executable by PUBLIC")
	}
	rows, err := env.App.Query(ctx, "SELECT * FROM runtime_sweep_candidates(interval '60 seconds')")
	if err != nil {
		t.Fatal(err)
	}
	fields := rows.FieldDescriptions()
	if len(fields) != 2 || fields[0].Name != "runtime_id" || fields[1].Name != "workspace_id" {
		t.Fatalf("discovery exposes extra metadata: %v", fields)
	}
	if !rows.Next() {
		t.Fatal("missing stale candidate")
	}
	var gotRuntime, gotWorkspace uuid.UUID
	if err := rows.Scan(&gotRuntime, &gotWorkspace); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if gotRuntime != runtime || gotWorkspace != ws {
		t.Fatal("wrong runtime identity")
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.workspace_id',$1,true)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, "UPDATE runtime SET status='offline' WHERE workspace_id=$1 AND id=$2", ws, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatal("foreign scope changed runtime")
	}
}
