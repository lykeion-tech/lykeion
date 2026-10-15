package migrations_test

import (
	"github.com/google/uuid"
	"lykeion/server/internal/testutil"
	"testing"
)

func TestStudyPropertiesWorkspaceIntegrity(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	user, a, b := uuid.New(), uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id,email) VALUES ($1,'registry@lab.org')`, user); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []uuid.UUID{a, b} {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id,slug,name) VALUES ($1,$2,'Lab')`, ws, ws.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Owner.Exec(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, user); err != nil {
			t.Fatal(err)
		}
	}
	var research, agent, card uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO research (workspace_id,name,created_by) VALUES ($1,'Methods',$2) RETURNING id`, b, user).Scan(&research); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,harness,owner_id) VALUES ($1,'Ada','codex',$2) RETURNING id`, b, user).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id,number,title,question,owner_id) VALUES ($1,1,'Study','How?',$2) RETURNING id`, a, user).Scan(&card); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		query string
		arg   any
	}{
		{`UPDATE card SET research_id=$2 WHERE id=$1`, research},
		{`UPDATE card SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, agent},
		{`UPDATE card SET assignee_type='member',assignee_id=$2 WHERE id=$1`, uuid.New()},
		{`UPDATE card SET assignee_type=NULL,assignee_id=$2 WHERE id=$1`, user},
		{`UPDATE card SET assignee_type='agent',assignee_id=NULL WHERE id=$1 AND owner_id=$2`, user},
	} {
		if _, err := env.Owner.Exec(ctx, bad.query, card, bad.arg); err == nil {
			t.Fatalf("accepted invalid link %s", bad.query)
		}
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, a.String()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"research", "agent"} {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("RLS exposed %d %s rows", count, table)
		}
	}
}
