package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestDecisionBuilderRollbacksProtectData(t *testing.T) {
	for _, test := range []struct {
		name, down string
		receipt    bool
	}{
		{"machine", "0059_decision_builder_machine.down.sql", false},
		{"receipt", "0060_decision_draft_receipt.down.sql", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testutil.Setup(t)
			owner, ws, _ := seedWorkspace(t, env)
			chat := uuid.New()
			if test.receipt {
				if _, err := env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id,purpose) VALUES($1,$2,$3,'manual_decision_draft')`, chat, ws, owner); err != nil {
					t.Fatal(err)
				}
				if _, err := env.Owner.Exec(t.Context(), `INSERT INTO decision_draft_receipt(workspace_id,owner_id,chat_id,decision_id,input_hash) VALUES($1,$2,$3,$4,decode('01','hex'))`, ws, owner, chat, uuid.New()); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id,purpose,builder_runtime_id) VALUES($1,$2,$3,'manual_decision_draft',$4)`, chat, ws, owner, uuid.New()); err != nil {
					t.Fatal(err)
				}
			}
			down, err := fs.ReadFile(migrations.FS, test.down)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = env.Owner.Exec(t.Context(), string(down)); err == nil {
				t.Fatal("rollback accepted protected builder data")
			}
			var count int
			if err = env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM lab_chat WHERE id=$1`, chat).Scan(&count); err != nil || count != 1 {
				t.Fatalf("draft changed after refused rollback: %d, %v", count, err)
			}
		})
	}
}

func TestDecisionDraftReceiptIsCreatorOnly(t *testing.T) {
	env := testutil.Setup(t)
	owner, ws, _ := seedWorkspace(t, env)
	other, chat := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'receipt-other@test')`, []any{other}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'admin')`, []any{ws, other}},
		{`INSERT INTO lab_chat(id,workspace_id,owner_id,purpose) VALUES($1,$2,$3,'manual_decision_draft')`, []any{chat, ws, owner}},
		{`INSERT INTO decision_draft_receipt(workspace_id,owner_id,chat_id,decision_id,input_hash) VALUES($1,$2,$3,$4,decode('01','hex'))`, []any{ws, owner, chat, uuid.New()}},
	} {
		if _, err := env.Owner.Exec(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		user uuid.UUID
		want int
	}{{owner, 1}, {other, 0}} {
		tx, err := env.App.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), test.user.String()); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM decision_draft_receipt`).Scan(&count); err != nil || count != test.want {
			t.Fatalf("receipt visibility %d want %d: %v", count, test.want, err)
		}
		_ = tx.Rollback(t.Context())
	}
}
