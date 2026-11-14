package service

import (
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func TestAristotleClaimMintsOnlyLabReadToken(t *testing.T) {
	f := setup(t)
	chat, run := uuid.New(), uuid.New()
	if _, e := f.Env.Owner.Exec(t.Context(), "INSERT INTO lab_chat(id,workspace_id,owner_id) VALUES($1,$2,$3)", chat, f.WS, f.Owner); e != nil {
		t.Fatal(e)
	}
	if _, e := f.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run(id,workspace_id,requester_id,responsible_user_id,kind,lab_chat_id,board_column,round,role,harness,prompt,status,attempt) VALUES($1,$2,$3,$3,'aristotle',$4,'',0,'aristotle','claude','q','dispatched',1)`, run, f.WS, f.Owner, chat); e != nil {
		t.Fatal(e)
	}
	err := f.T.Store.InWorkspace(t.Context(), f.WS, f.Owner, func(tx *store.Tx) error {
		r, e := tx.Q.GetAgentRun(t.Context(), db.GetAgentRunParams{WorkspaceID: f.WS, ID: run})
		if e != nil {
			return e
		}
		primary, call, e := issueClaimTokens(t.Context(), tx, r, true)
		if e != nil {
			return e
		}
		if primary == "" || call != "" {
			t.Fatal("Aristotle must have only its lab token")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var valid bool
	if err = f.Env.Owner.QueryRow(t.Context(), "SELECT count(*),bool_and(scopes=ARRAY['aristotle:ask']) FROM agent_run_token WHERE agent_run_id=$1", run).Scan(&count, &valid); err != nil || count != 1 || !valid {
		t.Fatalf("tokens=%d only_lab_scope=%v err=%v", count, valid, err)
	}
}
