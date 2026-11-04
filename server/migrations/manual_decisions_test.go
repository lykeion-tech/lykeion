package migrations_test

import (
	"github.com/google/uuid"
	"io/fs"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"strings"
	"testing"
)

func TestManualDecisionRLSCreatorOnly(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, creator, other, note := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'manual-creator@test'),($2,'manual-other@test')`, []any{creator, other}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'manual-privacy','manual')`, []any{ws}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'owner')`, []any{ws, creator, other}},
		{`INSERT INTO inbox_item(id,workspace_id,user_id,kind,text,title,context) VALUES($1,$2,$3,'manual_decision','','Private title','Private context')`, []any{note, ws, creator}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []uuid.UUID{creator, other} {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE id=$1`, note).Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 0
		if user == creator {
			want = 1
		}
		if count != want {
			t.Errorf("user %s sees %d private notes, want %d", user, count, want)
		}
		if user == other {
			tag, err := tx.Exec(ctx, `UPDATE inbox_item SET context='stolen' WHERE id=$1`, note)
			if err != nil {
				t.Fatal(err)
			}
			if tag.RowsAffected() != 0 {
				t.Error("noncreator updated note")
			}
		}
		tx.Rollback(ctx)
	}
}

func TestPrivateEventRLSAndReplay(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, creator, other := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'event-creator@test'),($2,'event-other@test')`, []any{creator, other}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'event-privacy','private')`, []any{ws}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'owner')`, []any{ws, creator, other}},
		{`INSERT INTO event(workspace_id,type,actor_type,recipient_id,payload) VALUES($1,'manual.created','user',$2,'{"secret":"private"}')`, []any{ws, creator}},
		{`INSERT INTO event(workspace_id,type,actor_type) VALUES($1,'shared','system')`, []any{ws}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []uuid.UUID{creator, other} {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM event WHERE workspace_id=$1`, ws).Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 1
		if user == creator {
			want = 2
		}
		if count != want {
			t.Errorf("visible events=%d, want %d", count, want)
		}
		tx.Rollback(ctx)
	}
}

func TestManualDecisionIdentityAndRoleMatrix(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, foreign, creator, card, note := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user"(id,email) VALUES($1,'matrix-creator@test');`, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace(id,slug,name) VALUES($1,'matrix','matrix'),($2,'foreign','foreign')`, ws, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, ws, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'study','q',$3,'grounding')`, card, ws, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,title) VALUES($1,$2,$3,$4,'manual_decision','','secret')`, note, ws, card, creator); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"study_owner", "member", "admin", "owner", "foreign"} {
		t.Run(role, func(t *testing.T) {
			user := uuid.New()
			if _, err := env.Owner.Exec(ctx, `INSERT INTO "user"(id,email) VALUES($1,$2)`, user, role+"@matrix.test"); err != nil {
				t.Fatal(err)
			}
			scope := ws
			memberRole := role
			if role == "foreign" {
				scope = foreign
				memberRole = "owner"
			}
			if role == "study_owner" {
				memberRole = "member"
				if _, err := env.Owner.Exec(ctx, `UPDATE card SET owner_id=$1 WHERE id=$2`, user, card); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := env.Owner.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,$3)`, scope, user, memberRole); err != nil {
				t.Fatal(err)
			}
			for _, sql := range []string{`SELECT count(*) FROM inbox_item WHERE id=$1`, `UPDATE inbox_item SET context='stolen' WHERE id=$1`, `DELETE FROM inbox_item WHERE id=$1`} {
				tx, err := env.App.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(sql, "SELECT") {
					var n int
					if err := tx.QueryRow(ctx, sql, note).Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n != 0 {
						t.Error("noncreator read linked note")
					}
				} else {
					tag, err := tx.Exec(ctx, sql, note)
					if err != nil {
						t.Fatal(err)
					}
					if tag.RowsAffected() != 0 {
						t.Error("noncreator mutated linked note")
					}
				}
				tx.Rollback(ctx)
			}
		})
	}
	for _, sql := range []string{
		`UPDATE inbox_item SET user_id=gen_random_uuid() WHERE id=$1`,
		`UPDATE inbox_item SET kind='blocked' WHERE id=$1`,
		`INSERT INTO inbox_item(workspace_id,user_id,kind,text,title) SELECT workspace_id,gen_random_uuid(),'manual_decision','','spoof' FROM inbox_item WHERE id=$1`,
	} {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), creator.String()); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, sql, note); err == nil {
			t.Errorf("accepted identity spoof: %s", sql)
		}
		tx.Rollback(ctx)
	}
}

func TestPrivateEventRecipientCannotBeRewritten(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, user, other := uuid.New(), uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user"(id,email) VALUES($1,'immutable@test'),($2,'spoof@test')`, user, other); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace(id,slug,name) VALUES($1,'immutable','immutable')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'owner')`, ws, user, other); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := env.Owner.QueryRow(ctx, `INSERT INTO event(workspace_id,recipient_id,type,actor_type) VALUES($1,$2,'manual.created','user') RETURNING id`, ws, user).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM lab_event_delivery WHERE event_id=$1`, id).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatal("private event entered shared maintenance queue")
	}
	for _, sql := range []string{
		`UPDATE event SET recipient_id=NULL WHERE id=$1`,
		`INSERT INTO event(workspace_id,recipient_id,type,actor_type) SELECT workspace_id,$2,'spoof','user' FROM event WHERE id=$1`,
	} {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
			t.Fatal(err)
		}
		args := []any{id}
		if strings.HasPrefix(sql, "INSERT") {
			args = append(args, other)
		}
		if _, err = tx.Exec(ctx, sql, args...); err == nil {
			t.Fatalf("accepted recipient mutation: %s", sql)
		}
		tx.Rollback(ctx)
	}
	if _, err := env.Owner.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, ws, user); err != nil {
		t.Fatal(err)
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM event WHERE id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("removed member retained private event access")
	}
}

func TestManualDecisionTitleBoundary(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, user := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user"(id,email) VALUES($1,'title-boundary@test')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace(id,slug,name) VALUES($1,'title-boundary','titles')`, ws); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"a\nb", "a\rb", strings.Repeat("x", 201), ""} {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO inbox_item(workspace_id,user_id,kind,text,title) VALUES($1,$2,'manual_decision','',$3)`, ws, user, title); err == nil {
			t.Errorf("accepted invalid title %q", title)
		}
	}
	for _, title := range []string{"normal title", strings.Repeat("x", 200)} {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO inbox_item(workspace_id,user_id,kind,text,title) VALUES($1,$2,'manual_decision','',$3)`, ws, user, title); err != nil {
			t.Errorf("rejected valid title %q: %v", title, err)
		}
	}
}

func TestManualDecisionMigrationRollback(t *testing.T) {
	env := testutil.Setup(t)
	for _, direction := range []string{"down", "up"} {
		sql, err := fs.ReadFile(migrations.FS, "0054_manual_decisions."+direction+".sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := env.Owner.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), string(sql)); err != nil {
			tx.Rollback(t.Context())
			t.Fatalf("%s: %v", direction, err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrivateEventHiddenAfterNoteDeletion(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, user, note := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'deleted-note@test')`, []any{user}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'deleted-note','deleted-note')`, []any{ws}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, []any{ws, user}},
		{`INSERT INTO inbox_item(id,workspace_id,user_id,kind,text,title) VALUES($1,$2,$3,'manual_decision','','private')`, []any{note, ws, user}},
		{`INSERT INTO event(workspace_id,recipient_id,type,actor_type,payload) VALUES($1,$2,'manual.created','user',jsonb_build_object('inbox_item_id',$3::text))`, []any{ws, user, note}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []int{1, 0} {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws.String(), user.String()); err != nil {
			t.Fatal(err)
		}
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM event WHERE workspace_id=$1`, ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("events=%d, want %d", n, want)
		}
		tx.Rollback(ctx)
		if _, err := env.Owner.Exec(ctx, `DELETE FROM inbox_item WHERE id=$1`, note); err != nil {
			t.Fatal(err)
		}
	}
}
