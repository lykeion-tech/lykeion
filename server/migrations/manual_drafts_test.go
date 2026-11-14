package migrations_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"io/fs"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"strings"
	"testing"
)

func TestManualDraftPurposeImmutable(t *testing.T) {
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
}

func TestManualDraftRollbackRefusesRetainedData(t *testing.T) {
	for _, state := range []string{"blank", "retained conversation", "tombstone", "normal chat draft residue", "normal message proposal residue"} {
		t.Run(state, func(t *testing.T) {
			env := testutil.Setup(t)
			who, ws, _ := seedWorkspace(t, env)
			chat := uuid.New()
			run := uuid.New()
			purpose := "manual_decision_draft"
			if strings.HasPrefix(state, "normal") {
				purpose = "answer"
			}
			if _, e := env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id,purpose,draft) VALUES($1,$2,$3,$4,'{"title":"private saved form","context":"","deadline":null,"card_id":null}')`, chat, ws, who, purpose); e != nil {
				t.Fatal(e)
			}
			if state != "blank" && state != "normal chat draft residue" {
				if _, e := env.Owner.Exec(t.Context(), `INSERT INTO agent_run(id,workspace_id,requester_id,responsible_user_id,lab_chat_id,kind,board_column,round,role,harness,prompt,lab_purpose,status) VALUES($1,$2,$3,$3,$4,'aristotle','',0,'aristotle','claude','private envelope',$5,'cancelled')`, run, ws, who, chat, purpose); e != nil {
					t.Fatal(e)
				}
				if _, e := env.Owner.Exec(t.Context(), `INSERT INTO lab_message(workspace_id,chat_id,owner_id,agent_run_id,kind,body,proposal,request_id) VALUES($1,$2,$3,$4,'question','private envelope','{"message":"private proposal"}',gen_random_uuid())`, ws, chat, who, run); e != nil {
					t.Fatal(e)
				}
				if _, e := env.Owner.Exec(t.Context(), `INSERT INTO agent_run_usage(workspace_id,agent_run_id,attempt,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd) VALUES($1,$2,1,'m',0,7,0,0,0.1)`, ws, run); e != nil {
					t.Fatal(e)
				}
			}
			if state == "tombstone" {
				if _, e := env.Owner.Exec(t.Context(), `UPDATE lab_chat SET deleted_at=now(),title='' WHERE id=$1;`, chat); e != nil {
					t.Fatal(e)
				}
				if _, e := env.Owner.Exec(t.Context(), `DELETE FROM lab_message WHERE chat_id=$1`, chat); e != nil {
					t.Fatal(e)
				}
				if _, e := env.Owner.Exec(t.Context(), `UPDATE agent_run SET prompt='',skill='',error='',session_id='' WHERE id=$1`, run); e != nil {
					t.Fatal(e)
				}
			}
			if state == "normal message proposal residue" {
				if _, e := env.Owner.Exec(t.Context(), `UPDATE lab_chat SET draft=NULL WHERE id=$1`, chat); e != nil {
					t.Fatal(e)
				}
			}
			snapshot := func() string {
				t.Helper()
				var out string
				e := env.Owner.QueryRow(t.Context(), `SELECT jsonb_build_object('chats',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM lab_chat c),'runs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM agent_run r),'messages',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM lab_message m),'usage',(SELECT jsonb_agg(to_jsonb(u)) FROM agent_run_usage u),'function',pg_get_functiondef('lab_record_answer(uuid,jsonb)'::regprocedure),'guard_function',pg_get_functiondef('lab_purpose_guard()'::regprocedure),'indexes',(SELECT jsonb_agg(indexdef ORDER BY indexname) FROM pg_indexes WHERE schemaname='public' AND tablename IN ('lab_chat','lab_message','agent_run')),'columns',(SELECT jsonb_agg(row_to_json(cols) ORDER BY table_name,ordinal_position) FROM information_schema.columns cols WHERE table_schema='public' AND table_name IN ('lab_chat','lab_message','agent_run')),'triggers',(SELECT jsonb_agg(pg_get_triggerdef(oid) ORDER BY tgrelid,tgname) FROM pg_trigger WHERE tgname='lab_purpose_guard'))::text`).Scan(&out)
				if e != nil {
					t.Fatal(e)
				}
				return out
			}
			before := snapshot()
			down, e := fs.ReadFile(migrations.FS, "0055_manual_decision_drafts.down.sql")
			if e != nil {
				t.Fatal(e)
			}
			_, e = env.Owner.Exec(t.Context(), string(down))
			var pgerr *pgconn.PgError
			if !errors.As(e, &pgerr) || pgerr.Code != "55000" || !strings.Contains(pgerr.Message, "drafting data") || pgerr.Hint == "" {
				t.Fatalf("rollback must refuse with an actionable error: %v", e)
			}
			if after := snapshot(); after != before {
				t.Fatal("refused rollback changed data, accounting or schema")
			}
		})
	}
}

func TestManualDraftRollbackWithoutDrafts(t *testing.T) {
	env := testutil.Setup(t)
	who, ws, _ := seedWorkspace(t, env)
	chat, run := uuid.New(), uuid.New()
	if _, e := env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id,title) VALUES($1,$2,$3,'normal chat')`, chat, ws, who); e != nil {
		t.Fatal(e)
	}
	if _, e := env.Owner.Exec(t.Context(), `INSERT INTO agent_run(id,workspace_id,requester_id,responsible_user_id,lab_chat_id,kind,board_column,round,role,harness,prompt,status) VALUES($1,$2,$3,$3,$4,'aristotle','',0,'aristotle','claude','normal question','cancelled')`, run, ws, who, chat); e != nil {
		t.Fatal(e)
	}
	if _, e := env.Owner.Exec(t.Context(), `INSERT INTO agent_run_usage(workspace_id,agent_run_id,attempt,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd) VALUES($1,$2,1,'m',0,7,0,0,0.1)`, ws, run); e != nil {
		t.Fatal(e)
	}
	if _, e := env.Owner.Exec(t.Context(), `INSERT INTO lab_message(workspace_id,chat_id,owner_id,agent_run_id,kind,body) VALUES($1,$2,$3,$4,'question','normal message')`, ws, chat, who, run); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"0057_manual_draft_application.down.sql", "0056_manual_draft_archives.down.sql", "0055_manual_decision_drafts.down.sql", "0055_manual_decision_drafts.up.sql", "0056_manual_draft_archives.up.sql", "0057_manual_draft_application.up.sql"} {
		raw, e := fs.ReadFile(migrations.FS, name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = env.Owner.Exec(t.Context(), string(raw)); e != nil {
			t.Fatalf("%s: %v", name, e)
		}
	}
	var valid bool
	if e := env.Owner.QueryRow(t.Context(), `SELECT c.purpose='answer' AND c.title='normal chat' AND r.lab_purpose='answer' AND r.prompt='normal question' AND u.output_tokens=7 AND EXISTS(SELECT 1 FROM lab_message m WHERE m.chat_id=c.id AND m.body='normal message') FROM lab_chat c JOIN agent_run r ON r.lab_chat_id=c.id JOIN agent_run_usage u ON u.agent_run_id=r.id WHERE c.id=$1`, chat).Scan(&valid); e != nil || !valid {
		t.Fatal("normal chat/accounting changed", e)
	}
}
