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

// Missing mode constraints, restrictive policies, immutable steps, or revocation
// cancellation must fail against real PostgreSQL rather than a schema snapshot.
func TestCowriteMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	owner, ws, _ := seedWorkspace(t, env)
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
	for _, table := range []string{"coresearch_change", "coresearch_comment", "coresearch_version"} {
		var composite bool
		if err := env.Owner.QueryRow(ctx, `SELECT EXISTS (
          SELECT 1 FROM pg_constraint c WHERE c.conrelid=($1::text)::regclass AND c.contype='u'
          AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY k(attnum,n)
            JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum ORDER BY k.n)
            = ARRAY['workspace_id','id']::text[])`, table).Scan(&composite); err != nil {
			t.Fatal(err)
		}
		if !composite {
			t.Errorf("%s lacks UNIQUE(workspace_id,id) composite FK target", table)
		}
	}
	member, outsider := uuid.New(), uuid.New()
	exec(`INSERT INTO "user"(id,email) VALUES ($1,'cowrite-member@x'),($2,'cowrite-outsider@x')`, member, outsider)
	exec(`INSERT INTO member(workspace_id,user_id,role) VALUES ($1,$2,'owner'),($1,$3,'member'),($1,$4,'member')`, ws, owner, member, outsider)
	experiment, project, private := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO coresearch_project(id,workspace_id,name,created_by) VALUES ($1,$2,'experiment',$3)`, experiment, ws, owner)
	refuse(`INSERT INTO coresearch_chat(workspace_id,project_id,owner_id,mode,cli) VALUES ($1,$2,$3,'cowrite','claude')`, "23503", ws, experiment, owner)
	exec(`INSERT INTO coresearch_project(id,workspace_id,name,created_by,mode,conversation_owner_id) VALUES ($1,$3,'shared',$4,'cowrite',NULL),($2,$3,'private',$4,'cowrite',$4)`, project, private, ws, owner)
	exec(`INSERT INTO coresearch_member(workspace_id,project_id,user_id,role) VALUES ($1,$2,$4,'owner'),($1,$2,$5,'member'),($1,$3,$4,'owner'),($1,$3,$5,'member')`, ws, project, private, owner, member)
	files, chats, proposals := [2]uuid.UUID{uuid.New(), uuid.New()}, [2]uuid.UUID{uuid.New(), uuid.New()}, [2]uuid.UUID{uuid.New(), uuid.New()}
	for i, p := range []uuid.UUID{project, private} {
		exec(`INSERT INTO coresearch_file(id,workspace_id,project_id,path) VALUES ($1,$2,$3,'main.tex')`, files[i], ws, p)
		exec(`INSERT INTO coresearch_chat(id,workspace_id,project_id,owner_id,mode,cli) VALUES ($1,$2,$3,$4,'cowrite','claude')`, chats[i], ws, p, owner)
		exec(`INSERT INTO coresearch_doc(workspace_id,file_id,checkpoint_text) VALUES ($1,$2,'paper')`, ws, files[i])
		exec(`INSERT INTO coresearch_doc_step(workspace_id,file_id,version,author_user_id,client_id,changes) VALUES ($1,$2,1,$3,'client','{}')`, ws, files[i], owner)
		exec(`INSERT INTO coresearch_proposal(id,workspace_id,project_id,chat_id,created_by) VALUES ($1,$2,$3,$4,$5)`, proposals[i], ws, p, chats[i], owner)
		exec(`INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES ($1,$2,$3,0,0,'','pending')`, ws, proposals[i], files[i])
		exec(`INSERT INTO coresearch_comment(workspace_id,file_id,from_pos,to_pos,quote,line,author_user_id,body) VALUES ($1,$2,0,1,'p',1,$3,'comment')`, ws, files[i], owner)
		exec(`INSERT INTO coresearch_build(workspace_id,project_id,seq,requested_by,status,sources) VALUES ($1,$2,1,$3,'queued','[]')`, ws, p, member)
		exec(`INSERT INTO coresearch_version(workspace_id,project_id,seq,title,by_user_id,files) VALUES ($1,$2,1,'first',$3,'[]')`, ws, p, owner)
	}
	visible := func(user uuid.UUID, index, want int) {
		t.Helper()
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
			t.Fatal(err)
		}
		p := []uuid.UUID{project, private}[index]
		for _, row := range []struct {
			table, key string
			id         uuid.UUID
		}{
			{"coresearch_doc", "file_id", files[index]}, {"coresearch_doc_step", "file_id", files[index]},
			{"coresearch_proposal", "project_id", p}, {"coresearch_change", "proposal_id", proposals[index]},
			{"coresearch_comment", "file_id", files[index]}, {"coresearch_build", "project_id", p}, {"coresearch_version", "project_id", p},
		} {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+row.table+` WHERE `+row.key+`=$1`, row.id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Fatalf("%s sees %d %s rows for project %d, want %d", user, n, row.table, index, want)
			}
		}
	}
	visible(owner, 0, 1)
	visible(member, 0, 1)
	visible(outsider, 0, 0)
	visible(owner, 1, 1)
	visible(member, 1, 0)
	refuse(`UPDATE coresearch_doc_step SET changes='[]' WHERE file_id=$1`, "23514", files[0])
	refuse(`DELETE FROM coresearch_doc_step WHERE file_id=$1`, "23514", files[0])
	refuse(`INSERT INTO coresearch_doc_step(workspace_id,file_id,version,client_id,changes) VALUES ($1,$2,2,'client','{}')`, "23514", ws, files[0])
	exec(`UPDATE coresearch_chat SET kernel_request='stop' WHERE id=$1`, chats[0])
	refuse(`UPDATE coresearch_chat SET kernel_request='pause' WHERE id=$1`, "23514", chats[0])
	refuse(`INSERT INTO coresearch_comment(workspace_id,file_id,from_pos,to_pos,quote,line,author_user_id,body) VALUES ($1,$2,0,1,$3,1,$4,'comment')`, "23514", ws, files[0], strings.Repeat("a", 4097), owner)
	refuse(`INSERT INTO coresearch_comment(workspace_id,file_id,from_pos,to_pos,quote,line,author_user_id,body) VALUES ($1,$2,0,1,'x',1,$3,$4)`, "23514", ws, files[0], owner, strings.Repeat("a", 16385))
	refuse(`INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES ($1,$2,$3,2,1,'','pending')`, "23514", ws, proposals[0], files[0])
	otherWS := uuid.New()
	exec(`INSERT INTO workspace(id,slug,name) VALUES ($1,'cowrite-other','other')`, otherWS)
	refuse(`UPDATE coresearch_doc SET workspace_id=$1 WHERE file_id=$2`, "23503", otherWS, files[0])
	refuse(`INSERT INTO coresearch_doc_step(workspace_id,file_id,version,author_user_id,client_id,changes) VALUES ($1,$2,2,$3,'x','[]')`, "23503", otherWS, files[0], owner)
	refuse(`INSERT INTO coresearch_comment(workspace_id,file_id,from_pos,to_pos,quote,line,author_user_id,body) VALUES ($1,$2,0,1,'x',1,$3,'x')`, "23503", otherWS, files[0], owner)
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), owner.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT * FROM coresearch_revoke($1,$2)`, member, project); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err = env.Owner.QueryRow(ctx, `SELECT status,failure_reason FROM coresearch_build WHERE project_id=$1`, project).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "aborted" || reason != "coresearch_access_revoked" {
		t.Fatalf("revoked build: %s / %s", status, reason)
	}
	exec(`UPDATE coresearch_build SET status='running' WHERE project_id=$1`, private)
	tx, err = env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), owner.String()); err != nil {
		t.Fatal(err)
	}
	var departed bool
	if err = tx.QueryRow(ctx, `SELECT coresearch_depart($1)`, member).Scan(&departed); err != nil {
		t.Fatal(err)
	}
	if !departed {
		t.Fatal("member departure refused")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = env.Owner.QueryRow(ctx, `SELECT status,failure_reason FROM coresearch_build WHERE project_id=$1`, private).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "aborted" || reason != "coresearch_access_revoked" {
		t.Fatalf("departed build: %s / %s", status, reason)
	}
}

func TestCowriteMigrationRollback(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	apply := func(direction string) {
		t.Helper()
		b, err := fs.ReadFile(migrations.FS, "0028_cowrite."+direction+".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = env.Owner.Exec(ctx, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string) string {
		t.Helper()
		var clause string
		if err := env.Owner.QueryRow(ctx, `SELECT check_clause FROM information_schema.check_constraints WHERE constraint_schema='public' AND constraint_name=$1`, name).Scan(&clause); err != nil {
			t.Fatal(err)
		}
		return clause
	}
	apply("down")
	owner, ws, _ := seedWorkspace(t, env)
	if _, err := env.Owner.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner');`, ws, owner); err != nil {
		t.Fatal(err)
	}
	request := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO coresearch_turn_receipt(workspace_id,owner_id,request_id,target,body,response) VALUES($1,$2,$3,'','legacy body','{}')`, ws, owner, request); err != nil {
		t.Fatal(err)
	}
	uniqueProject := func() bool {
		t.Helper()
		var present bool
		if err := env.Owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='coresearch_conversation'::regclass AND conname='coresearch_conversation_project_id_key')`).Scan(&present); err != nil {
			t.Fatal(err)
		}
		return present
	}
	if !uniqueProject() {
		t.Fatal("rollback did not restore one conversation per project")
	}
	mode, kernel := check("coresearch_chat_mode_check"), check("coresearch_chat_kernel_request_check")
	if strings.Contains(mode, "cowrite") || strings.Contains(kernel, "stop") {
		t.Fatalf("rollback retained widened checks: %s / %s", mode, kernel)
	}
	apply("up")
	if uniqueProject() {
		t.Fatal("Cowrite still restricts projects to one conversation")
	}
	var legacy string
	if err := env.Owner.QueryRow(ctx, `SELECT body::jsonb->>'Body' FROM coresearch_turn_receipt WHERE request_id=$1`, request).Scan(&legacy); err != nil || legacy != "legacy body" {
		t.Fatalf("receipt upgrade %q %v", legacy, err)
	}
	if !strings.Contains(check("coresearch_chat_mode_check"), "cowrite") || !strings.Contains(check("coresearch_chat_kernel_request_check"), "stop") {
		t.Fatal("reapply failed to widen checks")
	}
	apply("down")
	if err := env.Owner.QueryRow(ctx, `SELECT body FROM coresearch_turn_receipt WHERE request_id=$1`, request).Scan(&legacy); err != nil || legacy != "legacy body" {
		t.Fatalf("receipt rollback %q %v", legacy, err)
	}
	if !uniqueProject() {
		t.Fatal("rollback did not restore unique project")
	}
	if check("coresearch_chat_mode_check") != mode || check("coresearch_chat_kernel_request_check") != kernel {
		t.Fatal("rollback did not restore 0027 constraints")
	}
	apply("up")
}
