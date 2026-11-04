package migrations_test

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io/fs"
	"lykeion/server/internal/migrate"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"testing"
)

func TestLabChatDeletionUpgradeFrom0052AndRollback(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	down, err := fs.ReadFile(migrations.FS, "0053_lab_chat_deletion.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	archiveDown, archiveErr := fs.ReadFile(migrations.FS, "0056_manual_draft_archives.down.sql")
	if archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if _, archiveErr = conn.Exec(ctx, string(archiveDown)); archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if _, archiveErr = conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version=56`); archiveErr != nil {
		t.Fatal(archiveErr)
	}
	draftDown, e := fs.ReadFile(migrations.FS, "0055_manual_decision_drafts.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Exec(ctx, string(draftDown)); e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version=55`); e != nil {
		t.Fatal(e)
	}
	if _, err = conn.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version=53`); err != nil {
		t.Fatal(err)
	}
	u, ws, _ := seedWorkspace(t, env)
	id := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO lab_chat(id,workspace_id,owner_id,title) VALUES($1,$2,$3,'existing chat')`, id, ws, u); err != nil {
		t.Fatal(err)
	}
	if err = migrate.Up(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	var valid bool
	if err = conn.QueryRow(ctx, `SELECT deleted_at IS NULL AND title='existing chat' FROM lab_chat WHERE id=$1`, id).Scan(&valid); err != nil || !valid {
		t.Fatal(err, valid)
	}
	run := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO agent_run(id,workspace_id,requester_id,responsible_user_id,kind,lab_chat_id,board_column,round,role,harness,prompt,status) VALUES($1,$2,$3,$3,'aristotle',$4,'',1,'worker','claude','','cancelled')`, run, ws, u, id); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO agent_run_usage(workspace_id,agent_run_id,attempt,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd) VALUES($1,$2,1,'m',0,7,0,0,0.1)`, ws, run); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE lab_chat SET deleted_at=now(),title='' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	archiveDown, archiveErr = fs.ReadFile(migrations.FS, "0056_manual_draft_archives.down.sql")
	if archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if _, archiveErr = conn.Exec(ctx, string(archiveDown)); archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if _, archiveErr = conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version=56`); archiveErr != nil {
		t.Fatal(archiveErr)
	}
	draftDown, e = fs.ReadFile(migrations.FS, "0055_manual_decision_drafts.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Exec(ctx, string(draftDown)); e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version=55`); e != nil {
		t.Fatal(e)
	}
	if _, err = conn.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version=53`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1`, run).Scan(&n); err != nil || n != 1 {
		t.Fatal(err, n)
	}
	if err = migrate.Up(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
}
