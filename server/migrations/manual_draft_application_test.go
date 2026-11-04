package migrations_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"io/fs"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"testing"
)

func TestManualDraftApplicationRollbackRefusesRetainedState(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained", true: "tombstone"}[deleted], func(t *testing.T) {
			env := testutil.Setup(t)
			who, ws, _ := seedWorkspace(t, env)
			id := uuid.New()
			if _, e := env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id,purpose,applied_message_id,deleted_at) VALUES($1,$2,$3,'manual_decision_draft',gen_random_uuid(),CASE WHEN $4 THEN now() ELSE NULL END)`, id, ws, who, deleted); e != nil {
				t.Fatal(e)
			}
			var before string
			if e := env.Owner.QueryRow(t.Context(), `SELECT to_jsonb(c)::text FROM lab_chat c WHERE id=$1`, id).Scan(&before); e != nil {
				t.Fatal(e)
			}
			raw, e := fs.ReadFile(migrations.FS, "0057_manual_draft_application.down.sql")
			if e != nil {
				t.Fatal(e)
			}
			_, e = env.Owner.Exec(t.Context(), string(raw))
			var pe *pgconn.PgError
			if !errors.As(e, &pe) || pe.Code != "55000" || pe.Hint == "" {
				t.Fatal(e)
			}
			var after string
			if e = env.Owner.QueryRow(t.Context(), `SELECT to_jsonb(c)::text FROM lab_chat c WHERE id=$1 AND applied_message_id IS NOT NULL`, id).Scan(&after); e != nil || before != after {
				t.Fatal(e, after)
			}
			var constraint bool
			if e = env.Owner.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='lab_chat_applied_purpose')`).Scan(&constraint); e != nil || !constraint {
				t.Fatal(e, constraint)
			}
		})
	}
}
