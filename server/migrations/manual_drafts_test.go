package migrations_test

import (
	"github.com/google/uuid"
	"io/fs"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"testing"
)

func TestManualDraftPurposeAndRollback(t *testing.T) {
	env := testutil.Setup(t)
	who, ws, _ := seedWorkspace(t, env)
	chat := uuid.New()
	if _, e := env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id,purpose,draft) VALUES($1,$2,$3,'manual_decision_draft','{"title":"saved","context":"","deadline":null,"card_id":null}')`, chat, ws, who); e != nil {
		t.Fatal(e)
	}
	if _, e := env.Owner.Exec(t.Context(), "UPDATE lab_chat SET purpose='answer' WHERE id=$1", chat); e == nil {
		t.Fatal("chat purpose changed")
	}
	if _, e := env.Owner.Exec(t.Context(), `INSERT INTO agent_run(workspace_id,requester_id,responsible_user_id,lab_chat_id,kind,board_column,round,role,harness,prompt) VALUES($1,$2,$2,$3,'aristotle','',0,'aristotle','claude','')`, ws, who, chat); e == nil {
		t.Fatal("wrong-purpose run inserted")
	}
	var run uuid.UUID
	if e := env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run(workspace_id,requester_id,responsible_user_id,lab_chat_id,kind,board_column,round,role,harness,prompt,lab_purpose) VALUES($1,$2,$2,$3,'aristotle','',0,'aristotle','claude','','manual_decision_draft') RETURNING id`, ws, who, chat).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if _, e := env.Owner.Exec(t.Context(), "UPDATE agent_run SET lab_purpose='answer' WHERE id=$1", run); e == nil {
		t.Fatal("run purpose changed")
	}
	for _, name := range []string{"0055_manual_decision_drafts.down.sql", "0055_manual_decision_drafts.up.sql"} {
		raw, e := fs.ReadFile(migrations.FS, name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = env.Owner.Exec(t.Context(), string(raw)); e != nil {
			t.Fatalf("%s: %v", name, e)
		}
	}
	var purpose string
	if e := env.Owner.QueryRow(t.Context(), "SELECT purpose FROM lab_chat WHERE id=$1", chat).Scan(&purpose); e != nil || purpose != "answer" {
		t.Fatal(e, purpose)
	}
}
