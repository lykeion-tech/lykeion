package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestAristotleTables(t *testing.T) {
	env := testutil.Setup(t)
	for _, name := range []string{"lab_chat", "lab_message", "lab_read", "lesson", "lesson_flag", "lesson_counter", "lesson_job", "lesson_card"} {
		var rls, granted bool
		err := env.Owner.QueryRow(t.Context(), `SELECT relrowsecurity, has_table_privilege('lykeion_app',oid,'SELECT') AND has_table_privilege('lykeion_app',oid,'INSERT') AND CASE WHEN relname='lab_message' THEN
          has_column_privilege('lykeion_app',oid,'launch_sources','UPDATE') AND NOT has_column_privilege('lykeion_app',oid,'statements','UPDATE')
          ELSE has_table_privilege('lykeion_app',oid,'UPDATE') END
          AND has_table_privilege('lykeion_app',oid,'DELETE') FROM pg_class WHERE oid=to_regclass($1)`, name).Scan(&rls, &granted)
		if err != nil || !rls || !granted {
			t.Errorf("%s: RLS=%v app grant=%v err=%v", name, rls, granted, err)
		}
	}
	var accessible bool
	if err := env.Owner.QueryRow(t.Context(), `SELECT has_table_privilege('lykeion_app','lab_cursor','SELECT,INSERT,UPDATE,DELETE')`).Scan(&accessible); err != nil {
		t.Fatal(err)
	}
	if accessible {
		t.Fatal("app can directly access global cursor")
	}
}

func TestAgentRunAcceptsAristotle(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, _ := seedWorkspace(t, env)
	chat := uuid.New()
	if _, err := env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id) VALUES($1,$2,$3)`, chat, ws, u); err != nil {
		t.Fatal(err)
	}
	_, err := env.Owner.Exec(t.Context(), `INSERT INTO agent_run(workspace_id,requester_id,responsible_user_id,kind,lab_chat_id,board_column,round,role,harness,prompt) VALUES($1,$2,$2,'aristotle',$3,'',1,'worker','claude','q')`, ws, u, chat)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLabChatIsOwnerOnly(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	b, chat, run := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'lab-b@test')`, []any{b}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, []any{ws, b}},
		{`INSERT INTO lab_chat(id,workspace_id,owner_id) VALUES($1,$2,$3)`, []any{chat, ws, u}},
		{`INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body,context_card_id) VALUES($1,$2,$3,'question','q',$4)`, []any{ws, chat, u, card}},
		{`INSERT INTO agent_run(id,workspace_id,requester_id,responsible_user_id,kind,lab_chat_id,board_column,round,role,harness,prompt,status) VALUES($1,$2,$3,$3,'aristotle',$4,'',1,'worker','claude','q','running')`, []any{run, ws, u, chat}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		user uuid.UUID
		want int
	}{{u, 1}, {b, 0}, {run, 1}} {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), tc.user.String()); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"lab_chat", "lab_message"} {
			var n int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != tc.want {
				t.Errorf("%s user %s: got %d want %d", table, tc.user, n, tc.want)
			}
		}
		tx.Rollback(ctx)
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET status='completed' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), run.String())
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM lab_chat`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("completed run sees %d: %v", n, err)
	}
}

func TestLabEventsAfterAdvances(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	var ids []int64
	for i := 0; i < 2; i++ {
		ws := uuid.New()
		if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace(id,slug,name) VALUES($1,$2,'lab')`, ws, ws.String()); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := env.Owner.QueryRow(ctx, `INSERT INTO event(workspace_id,type,actor_type) VALUES($1,'lab.test','system') RETURNING id`, ws).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	check := func(want int) {
		t.Helper()
		var n int
		if err := env.App.QueryRow(ctx, `SELECT count(*) FROM lab_events_after('t',10)`).Scan(&n); err != nil || n != want {
			t.Fatalf("events=%d want=%d err=%v", n, want, err)
		}
	}
	check(2)
	if _, err := env.App.Exec(ctx, `SELECT lab_advance('t',$1)`, ids[0]); err != nil {
		t.Fatal(err)
	}
	check(1)
	if _, err := env.App.Exec(ctx, `SELECT lab_advance('t',0)`); err != nil {
		t.Fatal(err)
	}
	check(1)
}

func TestAristotleMigrationReversible(t *testing.T) {
	env := testutil.Setup(t)
	for _, name := range []string{"0052_aristotle.down.sql", "0052_aristotle.up.sql"} {
		q, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = env.Owner.Exec(t.Context(), string(q)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, table := range []string{"lab_event_delivery", "lab_fact_dirty"} {
			var exists bool
			if err := env.Owner.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != (name == "0052_aristotle.up.sql") {
				t.Fatalf("%s after %s exists=%v", table, name, exists)
			}
			if exists {
				var protected bool
				if err := env.Owner.QueryRow(t.Context(), `SELECT relrowsecurity AND EXISTS(SELECT 1 FROM pg_policy WHERE polrelid=c.oid) AND NOT has_table_privilege('lykeion_app',c.oid,'SELECT,INSERT,UPDATE,DELETE') FROM pg_class c WHERE oid=$1::regclass`, table).Scan(&protected); err != nil || !protected {
					t.Fatalf("%s security after reapply=%v err=%v", table, protected, err)
				}
			}
		}
	}
}

func TestAristotleWorkspaceReferences(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	other, chat, lesson, decision, run := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'lab-other','Other')`, []any{other}},
		{`INSERT INTO lab_chat(id,workspace_id,owner_id) VALUES($1,$2,$3)`, []any{chat, ws, u}},
		{`INSERT INTO lesson(id,workspace_id,kind,text,model) VALUES($1,$2,'lab','lesson','model')`, []any{lesson, ws}},
		{`INSERT INTO inbox_item(id,workspace_id,user_id,kind,text) VALUES($1,$2,$3,'done','decision')`, []any{decision, ws, u}},
		{`INSERT INTO agent_run(id,workspace_id,requester_id,responsible_user_id,kind,lab_chat_id,board_column,round,role,harness,prompt) VALUES($1,$2,$3,$3,'aristotle',$4,'',1,'worker','claude','q')`, []any{run, ws, u, chat}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{"message chat", `INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body) VALUES($1,$2,$3,'question','q')`, []any{other, chat, u}},
		{"message owner", `INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body) VALUES($1,$2,$3,'question','q')`, []any{ws, chat, uuid.New()}},
		{"run chat", `INSERT INTO agent_run(workspace_id,requester_id,responsible_user_id,kind,lab_chat_id,board_column,round,role,harness,prompt) VALUES($1,$2,$2,'aristotle',$3,'',1,'worker','claude','q')`, []any{other, u, chat}},
		{"read run", `INSERT INTO lab_read(workspace_id,agent_run_id,ref) VALUES($1,$2,'ref')`, []any{other, run}},
		{"flag lesson", `INSERT INTO lesson_flag(workspace_id,lesson_id,user_id) VALUES($1,$2,$3)`, []any{other, lesson, u}},
		{"counter lesson", `INSERT INTO lesson_counter(workspace_id,lesson_id,ref) VALUES($1,$2,'ref')`, []any{other, lesson}},
		{"job lesson", `INSERT INTO lesson_job(workspace_id,lesson_id,due_at) VALUES($1,$2,now())`, []any{other, lesson}},
		{"job card", `INSERT INTO lesson_job(workspace_id,card_id,due_at) VALUES($1,$2,now())`, []any{other, card}},
		{"job decision", `INSERT INTO lesson_job(workspace_id,decision_id,due_at) VALUES($1,$2,now())`, []any{other, decision}},
		{"lesson card", `INSERT INTO lesson_card(workspace_id,card_id,board_column) VALUES($1,$2,'grounding')`, []any{other, card}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.Owner.Exec(ctx, tc.sql, tc.args...)
			wantCode(t, err, "23503", tc.name)
		})
	}
}

func TestAristotleDueJobs(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	_, ws, _ := seedWorkspace(t, env)
	for _, q := range []string{
		`INSERT INTO lesson_job(workspace_id,due_at) VALUES($1,now()-interval '1 hour')`,
		`INSERT INTO lesson_job(workspace_id,due_at) VALUES($1,now()+interval '1 hour')`,
		`INSERT INTO lesson_job(workspace_id,due_at,status) VALUES($1,now()-interval '1 hour','done')`,
	} {
		if _, err := env.Owner.Exec(ctx, q, ws); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM due_lesson_jobs(10)`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("due jobs=%d err=%v", n, err)
	}
	for _, name := range []string{"lab_events_after(text,integer)", "lab_advance(text,bigint)", "due_lesson_jobs(integer)", "lab_run_can_read(uuid,uuid)"} {
		var public bool
		if err := env.Owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc p, LATERAL aclexplode(p.proacl) a WHERE p.oid=$1::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`, name).Scan(&public); err != nil {
			t.Fatal(err)
		}
		if public {
			t.Errorf("PUBLIC can execute %s", name)
		}
	}
}

func TestAristotleFunctionsIgnoreTemporaryTables(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	owner, ws, _ := seedWorkspace(t, env)
	chat, attacker := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO lab_chat(id,workspace_id,owner_id) VALUES($1,$2,$3)`, []any{chat, ws, owner}},
		{`INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body) VALUES($1,$2,$3,'question','private')`, []any{ws, chat, owner}},
		{`INSERT INTO event(workspace_id,type,actor_type) VALUES($1,'lab.shadow','system')`, []any{ws}},
		{`INSERT INTO lesson_job(workspace_id,due_at) VALUES($1,now())`, []any{ws}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), attacker.String()); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TEMP TABLE agent_run (id uuid,workspace_id uuid,kind text,status text,lab_chat_id uuid,requester_id uuid) ON COMMIT DROP`,
		`CREATE TEMP TABLE event (LIKE public.event) ON COMMIT DROP`,
		`CREATE TEMP TABLE lab_cursor (consumer text PRIMARY KEY,last_event_id bigint NOT NULL) ON COMMIT DROP`,
		`CREATE TEMP TABLE lesson_job (LIKE public.lesson_job) ON COMMIT DROP`,
		`INSERT INTO pg_temp.lab_cursor(consumer,last_event_id) VALUES('shadow',9223372036854775807)`,
	} {
		if _, err = tx.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pg_temp.agent_run VALUES($1,$2,'aristotle','running',$3,$4)`, attacker, ws, chat, owner); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"lab_chat", "lab_message"} {
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM public.`+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("forged temporary run reveals %d %s rows", n, table)
		}
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM public.lab_events_after('shadow',10)`).Scan(&n); err != nil || n != 1 {
		t.Errorf("shadowed event/cursor: n=%d err=%v", n, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM public.due_lesson_jobs(10)`).Scan(&n); err != nil || n != 1 {
		t.Errorf("shadowed jobs: n=%d err=%v", n, err)
	}
	if _, err = tx.Exec(ctx, `SELECT public.lab_advance('shadow',17)`); err != nil {
		t.Fatal(err)
	}
	var last int64
	if err = tx.QueryRow(ctx, `SELECT last_event_id FROM pg_temp.lab_cursor WHERE consumer='shadow'`).Scan(&last); err != nil || last != 9223372036854775807 {
		t.Fatalf("temporary cursor mutated: last=%d err=%v", last, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = env.Owner.QueryRow(ctx, `SELECT last_event_id FROM public.lab_cursor WHERE consumer='shadow'`).Scan(&last); err != nil || last != 17 {
		t.Fatalf("public cursor not advanced: last=%d err=%v", last, err)
	}
}

// Queue tables remain owner-only; tenant policies also protect against accidental
// future grants. Definer helpers intentionally process work across tenants.
func TestAristotleQueueSecurity(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	workspaces := []uuid.UUID{uuid.New(), uuid.New()}
	for _, ws := range workspaces {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace(id,slug,name) VALUES($1,$2,'queue')`, ws, ws.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Owner.Exec(ctx, `INSERT INTO event(workspace_id,type,actor_type) VALUES($1,'queue.test','system')`, ws); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Owner.Exec(ctx, `INSERT INTO lab_fact_dirty(consumer,workspace_id,ref) VALUES('indexer',$1,'test')`, ws); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"lab_event_delivery", "lab_fact_dirty"} {
		var rls, policy, granted bool
		if err := env.Owner.QueryRow(ctx, `SELECT relrowsecurity, EXISTS(SELECT 1 FROM pg_policy WHERE polrelid=c.oid), has_table_privilege('lykeion_app',c.oid,'SELECT,INSERT,UPDATE,DELETE') FROM pg_class c WHERE oid=$1::regclass`, table).Scan(&rls, &policy, &granted); err != nil {
			t.Fatal(err)
		}
		if !rls || !policy || granted {
			t.Fatalf("%s: RLS=%v policy=%v app grant=%v", table, rls, policy, granted)
		}
		for _, query := range []string{"SELECT * FROM " + table, "DELETE FROM " + table, "UPDATE " + table + " SET workspace_id=workspace_id", "INSERT INTO " + table + " SELECT * FROM " + table + " WHERE false"} {
			_, err := env.App.Exec(ctx, query)
			wantCode(t, err, "42501", query)
		}
		// Temporarily grant access inside a rolled-back owner transaction to exercise
		// the policy independently of the stronger owner-only privilege boundary.
		tx, err := env.Owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO lykeion_app"); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE lykeion_app"); err != nil {
			t.Fatal(err)
		}
		var n int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s unscoped rows=%d err=%v", table, n, err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, workspaces[0].String()); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s scoped rows=%d err=%v", table, n, err)
		}
		_, err = tx.Exec(ctx, "UPDATE "+table+" SET workspace_id=$1", workspaces[1])
		wantCode(t, err, "42501", table+" cross-workspace update")
		tx.Rollback(ctx)
	}
	var n int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM lab_pending_events('indexer',200)`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("definer event rows=%d err=%v", n, err)
	}
	for _, ws := range workspaces {
		if _, err := env.App.Exec(ctx, `SELECT lab_ack_work('indexer',ARRAY(SELECT id FROM lab_pending_workspace_work('indexer',$1,200) WHERE source='event'),ARRAY(SELECT id FROM lab_pending_workspace_work('indexer',$1,200) WHERE source='dirty'))`, ws); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.Owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM lab_event_delivery)+(SELECT count(*) FROM lab_fact_dirty)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("owner remaining queue rows=%d err=%v", n, err)
	}
}
