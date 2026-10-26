package migrations_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestCoresearchMigration(t *testing.T) {
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
	kindCheck := func() string {
		t.Helper()
		var check string
		if err := env.Owner.QueryRow(ctx, `SELECT check_clause FROM information_schema.check_constraints WHERE constraint_schema='public' AND constraint_name='agent_run_kind_check'`).Scan(&check); err != nil {
			t.Fatal(err)
		}
		return check
	}
	// Missing tables, permissive membership policies, and a broken rollback each
	// fail against real PostgreSQL rather than a textual schema snapshot.
	exec(`INSERT INTO coresearch_project (workspace_id,name,created_by) VALUES ($1,'probe',$2)`, ws, u)
	exec(`DELETE FROM coresearch_project`)
	original := kindCheck()
	for _, direction := range []string{"down", "up"} {
		b, err := fs.ReadFile(migrations.FS, "0025_coresearch."+direction+".sql")
		if err != nil {
			t.Fatal(err)
		}
		exec(string(b))
		if direction == "down" {
			if strings.Contains(kindCheck(), "coresearch") {
				t.Fatal("down retained coresearch kind")
			}
		}
	}
	if got := kindCheck(); got != original {
		t.Fatalf("kind check after up/down/up: %q, want %q", got, original)
	}
	outsider := uuid.New()
	exec(`INSERT INTO "user" (id,email) VALUES ($1,'outsider@x')`, outsider)
	exec(`INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner'),($1,$3,'member')`, ws, u, outsider)
	projects, files, chats, runs := [2]uuid.UUID{uuid.New(), uuid.New()}, [2]uuid.UUID{uuid.New(), uuid.New()}, [2]uuid.UUID{uuid.New(), uuid.New()}, [2]uuid.UUID{uuid.New(), uuid.New()}
	for i := range projects {
		exec(`INSERT INTO coresearch_project (id,workspace_id,name,created_by) VALUES ($1,$2,'project',$3)`, projects[i], ws, u)
		who := u
		if i == 1 {
			who = outsider
		}
		exec(`INSERT INTO coresearch_member (workspace_id,project_id,user_id,role) VALUES ($1,$2,$3,'owner')`, ws, projects[i], who)
		exec(`INSERT INTO coresearch_file (id,workspace_id,project_id,path) VALUES ($1,$2,$3,'script.py')`, files[i], ws, projects[i])
		exec(`INSERT INTO coresearch_file_version (workspace_id,file_id,n,sha256,size,blob_key,author_user_id) VALUES ($1,$2,1,$3,1,'blob',$4)`, ws, files[i], strings.Repeat("a", 64), who)
		exec(`INSERT INTO coresearch_chat (id,workspace_id,project_id,owner_id,mode,cli) VALUES ($1,$2,$3,$4,'experiment','claude')`, chats[i], ws, projects[i], who)
		exec(`INSERT INTO coresearch_message (workspace_id,chat_id,kind,body) VALUES ($1,$2,'comment','hello')`, ws, chats[i])
		exec(`INSERT INTO coresearch_cell (workspace_id,chat_id,seq,kernel,source,status) VALUES ($1,$2,1,'python','print(1)','queued')`, ws, chats[i])
		exec(`INSERT INTO coresearch_run (id,workspace_id,project_id,key,chat_id,script_file_id,script_n,inputs,status,requested_by) VALUES ($1,$2,$3,'R1',$4,$5,1,'[]','queued',$6)`, runs[i], ws, projects[i], chats[i], files[i], who)
		exec(`INSERT INTO coresearch_setup (workspace_id,project_id,chat_id,status,requested_by) VALUES ($1,$2,$3,'queued',$4)`, ws, projects[i], chats[i], who)
	}
	var agent uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id,kind,requester_id,responsible_user_id,coresearch_chat_id,board_column,round,role,harness,prompt,status) VALUES ($1,'coresearch',$2,$2,$3,'',0,'coresearch','claude','p','running') RETURNING id`, ws, u, chats[0]).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	visible := func(user uuid.UUID, want int) {
		t.Helper()
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct {
			table, key string
			id         uuid.UUID
		}{
			{"coresearch_project", "id", projects[0]},
			{"coresearch_member", "project_id", projects[0]},
			{"coresearch_file", "project_id", projects[0]},
			{"coresearch_file_version", "file_id", files[0]},
			{"coresearch_chat", "project_id", projects[0]},
			{"coresearch_message", "chat_id", chats[0]},
			{"coresearch_cell", "chat_id", chats[0]},
			{"coresearch_run", "project_id", projects[0]},
			{"coresearch_setup", "project_id", projects[0]},
		} {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+row.table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Fatalf("%s sees %d %s rows, want %d", user, n, row.table, want)
			}
			expected := want
			if user == outsider {
				expected = 0
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+row.table+` WHERE `+row.key+`=$1`, row.id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != expected {
				t.Fatalf("%s sees %d first-project %s rows, want %d", user, n, row.table, expected)
			}
		}
	}
	visible(u, 1)
	visible(outsider, 1)
	visible(uuid.New(), 0)
	visible(agent, 1)
	exec(`UPDATE agent_run SET status='completed' WHERE id=$1`, agent)
	visible(agent, 0)
	refuse(`INSERT INTO agent_run (workspace_id,card_id,kind,requester_id,responsible_user_id,coresearch_chat_id,board_column,round,role,harness,prompt) VALUES ($1,$2,'coresearch',$3,$3,$4,'',0,'coresearch','claude','p')`, "23514", ws, card, u, chats[0])
	refuse(`UPDATE agent_run SET kind='role',card_id=$2 WHERE id=$1`, "23514", agent, card)
	refuse(`UPDATE agent_run SET kind='autoresearch' WHERE id=$1`, "23514", agent)
	refuse(`UPDATE agent_run SET requester_id=NULL WHERE id=$1`, "23514", agent)
	refuse(`UPDATE coresearch_chat SET mode='cowrite' WHERE id=$1`, "23514", chats[0])
	refuse(`UPDATE coresearch_file_version SET sha256='x' WHERE file_id=$1`, "23514", files[0])
	otherWS := uuid.New()
	exec(`INSERT INTO workspace (id,slug,name) VALUES ($1,'other','other')`, otherWS)
	refuse(`INSERT INTO coresearch_file (workspace_id,project_id,path) VALUES ($1,$2,'cross.py')`, "23503", otherWS, projects[0])
	refuse(`INSERT INTO coresearch_member (workspace_id,project_id,user_id,role) VALUES ($1,$2,$3,'member')`, "23503", ws, projects[0], uuid.New())
	for _, kind := range []string{"reply", "failure"} {
		exec(`INSERT INTO coresearch_message (workspace_id,chat_id,kind,agent_run_id,body) VALUES ($1,$2,$3,$4,'done')`, ws, chats[0], kind, agent)
		refuse(`INSERT INTO coresearch_message (workspace_id,chat_id,kind,agent_run_id,body) VALUES ($1,$2,$3,$4,'duplicate')`, "23505", ws, chats[0], kind, agent)
	}
}
