package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	db "lykeion/server/pkg/db/generated"
)

func TestSharedMachinesMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(sql, code string, args ...any) {
		t.Helper()
		_, err := env.Owner.Exec(ctx, sql, args...)
		pgerr, ok := err.(*pgconn.PgError)
		if !ok || pgerr.Code != code {
			t.Fatalf("want SQLSTATE %s, got %v", code, err)
		}
	}
	var runtime uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1,$2,'machine','claude') RETURNING id`, ws, u).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for range 2 {
		exec(read("0023_shared_machines.down.sql"))
		exec(read("0023_shared_machines.up.sql"))
	}
	var visibility string
	if err := env.Owner.QueryRow(ctx, `SELECT visibility FROM runtime WHERE id=$1`, runtime).Scan(&visibility); err != nil || visibility != "private" {
		t.Fatalf("visibility=%q (%v), want private", visibility, err)
	}
	refuse(`UPDATE runtime SET visibility='public' WHERE id=$1`, "23514", runtime)
	exec(`INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, u)
	other := uuid.New()
	exec(`INSERT INTO workspace (id,slug,name) VALUES ($1,'other','other')`, other)
	exec(`INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'member')`, other, u)
	refuse(`INSERT INTO runtime_share (workspace_id,runtime_id,user_id) VALUES ($1,$2,$3)`, "23503", other, runtime, u)
	outsider := uuid.New()
	exec(`INSERT INTO "user" (id,email) VALUES ($1,'outsider@x')`, outsider)
	refuse(`INSERT INTO runtime_share (workspace_id,runtime_id,user_id) VALUES ($1,$2,$3)`, "23503", ws, runtime, outsider)
	exec(`INSERT INTO runtime_share (workspace_id,runtime_id,user_id) VALUES ($1,$2,$3)`, ws, runtime, u)
	exec(`DELETE FROM runtime WHERE id=$1`, runtime)
	var count int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM runtime_share`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("shares=%d (%v), want 0", count, err)
	}
	exec(`INSERT INTO agent_run (workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,failure_reason,cancel_reason,machine_name,machine_owner_id) VALUES ($1,$2,$3,'grounding',1,'scoper','claude','p','runtime_access_denied','runtime_access_revoked','machine',$3)`, ws, card, u)
	refuse(`UPDATE agent_run SET cancel_reason='x'`, "23514")
}

func TestSharedMachinesQueriesAndRLS(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, u)
	q := db.New(env.Owner)
	rt, err := q.UpsertRuntime(ctx, db.UpsertRuntimeParams{WorkspaceID: ws, OwnerID: u, DaemonID: "machine", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListRuntimes(ctx, ws)
	if err != nil || len(rows) != 1 || len(rows[0].SharedWith) != 0 {
		t.Fatalf("empty shares=%+v (%v)", rows, err)
	}
	for range 2 {
		if err := q.ReplaceRuntimeShares(ctx, db.ReplaceRuntimeSharesParams{WorkspaceID: ws, RuntimeID: rt.ID, UserIds: []uuid.UUID{u}}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = q.ListRuntimes(ctx, ws)
	if err != nil || len(rows) != 1 || len(rows[0].SharedWith) != 1 || rows[0].SharedWith[0] != u {
		t.Fatalf("listed shares=%+v (%v)", rows, err)
	}
	shares, err := q.RuntimeShares(ctx, db.RuntimeSharesParams{WorkspaceID: ws, RuntimeID: rt.ID})
	if err != nil || len(shares) != 1 || shares[0] != u {
		t.Fatalf("shares=%+v (%v)", shares, err)
	}
	for _, owner := range []uuid.UUID{uuid.New(), u} {
		n, err := q.SetRuntimeSharing(ctx, db.SetRuntimeSharingParams{WorkspaceID: ws, ID: rt.ID, OwnerID: owner, Visibility: "workspace"})
		want := int64(0)
		if owner == u {
			want = 1
		}
		if err != nil || n != want {
			t.Fatalf("sharing updated=%d (%v), want %d", n, err, want)
		}
	}
	var count int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM runtime_share`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unscoped shares=%d (%v)", count, err)
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, ws.String()); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM runtime_share`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("tenant shares=%d (%v)", count, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE runtime_share SET workspace_id=$1`, uuid.New()); err == nil {
		t.Fatal("RLS accepted other workspace share")
	} else if pgerr, ok := err.(*pgconn.PgError); !ok || pgerr.Code != "42501" {
		t.Fatalf("want RLS refusal 42501, got %v", err)
	}
	tx.Rollback(ctx)
	if err = q.ReplaceRuntimeShares(ctx, db.ReplaceRuntimeSharesParams{WorkspaceID: ws, RuntimeID: rt.ID, UserIds: []uuid.UUID{}}); err != nil {
		t.Fatal(err)
	}
	shares, err = q.RuntimeShares(ctx, db.RuntimeSharesParams{WorkspaceID: ws, RuntimeID: rt.ID})
	if err != nil || len(shares) != 0 {
		t.Fatalf("cleared shares=%+v (%v)", shares, err)
	}
	machine := "machine"
	created, err := q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: ws, Number: 2, Title: "new", Question: "q", OwnerID: u, BoardColumn: "grounding", Machine: &machine})
	if err != nil || created.Machine == nil || *created.Machine != machine {
		t.Fatalf("created machine=%v (%v)", created.Machine, err)
	}
	n, err := q.SetCardMachine(ctx, db.SetCardMachineParams{WorkspaceID: ws, ID: card, Machine: &machine})
	if err != nil || n != 1 {
		t.Fatalf("set machine=%d (%v)", n, err)
	}
	n, err = q.SetCardMachine(ctx, db.SetCardMachineParams{WorkspaceID: uuid.New(), ID: card})
	if err != nil || n != 0 {
		t.Fatalf("cross-workspace set=%d (%v)", n, err)
	}
	n, err = q.SetCardMachine(ctx, db.SetCardMachineParams{WorkspaceID: ws, ID: card})
	if err != nil || n != 1 {
		t.Fatalf("clear machine=%d (%v)", n, err)
	}
	got, err := q.GetCard(ctx, db.GetCardParams{WorkspaceID: ws, ID: card})
	if err != nil || got.Machine != nil {
		t.Fatalf("cleared machine=%v (%v)", got.Machine, err)
	}
}
