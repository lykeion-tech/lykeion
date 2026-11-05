package migrations_test

import (
	"github.com/google/uuid"
	"io/fs"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"testing"
)

func TestCommentReservationWorkspaceIntegrity(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	user, a, b, card, run, comment := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'reservations@lab.org')`, []any{user}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1::uuid,$1::uuid::text,'A'),($2::uuid,$2::uuid::text,'B')`, []any{a, b}},
		{`INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'Study','Question',$3,'grounding')`, []any{card, a, user}},
		{`INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt) VALUES($1,$2,$3,$4,'grounding',1,'leader','codex','prompt')`, []any{run, a, card, user}},
		{`INSERT INTO comment(id,workspace_id,card_id,author_id,body) VALUES($1,$2,$3,$4,'message')`, []any{comment, a, card, user}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := `INSERT INTO comment_reservation(workspace_id,comment_id,agent_run_id,attempt) VALUES($1,$2,$3,$4)`
	if _, err := env.Owner.Exec(ctx, insert, b, comment, run, 1); err == nil {
		t.Fatal("cross-workspace reservation accepted")
	}
	if _, err := env.Owner.Exec(ctx, insert, a, comment, run, 0); err == nil {
		t.Fatal("zero attempt accepted")
	}
	if _, err := env.Owner.Exec(ctx, insert, a, comment, run, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, insert, a, comment, run, 2); err == nil {
		t.Fatal("duplicate reservation accepted")
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, b.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM comment_reservation`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("RLS leaked %d: %v", n, err)
	}
	if _, err = tx.Exec(ctx, insert, a, comment, run, 1); err == nil {
		t.Fatal("RLS permitted foreign workspace write")
	}
}

func TestQueuedLegacyCommentReceiptBecomesReservation(t *testing.T) {
	for _, role := range []string{"leader", "agent", "member-1", "auditor"} {
		t.Run(role, func(t *testing.T) {
			env := testutil.Setup(t)
			ctx := t.Context()
			down, err := fs.ReadFile(migrations.FS, "0063_comment_reservations.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			up, err := fs.ReadFile(migrations.FS, "0063_comment_reservations.up.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = env.Owner.Exec(ctx, string(down)); err != nil {
				t.Fatal(err)
			}
			user, ws, card := seedWorkspace(t, env)
			run, comment := uuid.New(), uuid.New()
			if _, err = env.Owner.Exec(ctx, `INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt) VALUES($1,$2,$3,$4,'grounding',1,$5,'codex','snapshot')`, run, ws, card, user, role); err != nil {
				t.Fatal(err)
			}
			if _, err = env.Owner.Exec(ctx, `INSERT INTO comment(id,workspace_id,card_id,author_id,body) VALUES($1,$2,$3,$4,'pending')`, comment, ws, card, user); err != nil {
				t.Fatal(err)
			}
			if _, err = env.Owner.Exec(ctx, `INSERT INTO record(workspace_id,card_id,board_column,round,kind,role,body,agent_run_id) VALUES($1,$2,'grounding',1,'comment_delivery','leader',jsonb_build_object('comment_id',$3::text,'how','next_run'),$4)`, ws, card, comment, run); err != nil {
				t.Fatal(err)
			}
			if _, err = env.Owner.Exec(ctx, string(up)); err != nil {
				t.Fatal(err)
			}
			var pending, retained int
			if err = env.Owner.QueryRow(ctx, `SELECT count(*) FROM comment_reservation WHERE comment_id=$1 AND attempt=1 AND acknowledged_at IS NULL`, comment).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if err = env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND body->>'historical_unconfirmed'='true'`, run).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			want := 0
			if role == "leader" || role == "agent" {
				want = 1
			}
			if pending != want || retained != 1 {
				t.Fatalf("pending=%d retained=%d", pending, retained)
			}
		})
	}
}
