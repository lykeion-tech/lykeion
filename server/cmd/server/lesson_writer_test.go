package main

import (
	"errors"
	"github.com/google/uuid"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"testing"
)

func TestNoKeysNoWriter(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, user, id := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'test','Test')`, []any{ws}},
		{`INSERT INTO "user"(id,email,name) VALUES($1,'reader@example.test','Reader')`, []any{user}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, []any{ws, user}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	base := &store.Store{Pool: env.App}
	err := base.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		ver, err := labmemory.Version(ctx, tx, labmemory.Ref{Kind: "workspace"})
		if err != nil {
			return err
		}
		_, err = env.Owner.Exec(ctx, `INSERT INTO lesson(id,workspace_id,kind,text,cites,model,checked_at) VALUES($1,$2,'lab','active',jsonb_build_array(jsonb_build_object('ref','workspace:context','version',$3::text)),'test',now())`, id, ws, ver)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][2]string{{"haiku", "jev"}, {"", "jev"}, {"haiku", " "}, {"", ""}, {"haiku", "jev"}} {
		st := &store.Store{Pool: env.App}
		enabled := keys[0] == "haiku" && keys[1] == "jev"
		if (buildLessonWriter(st, nil, keys[0], keys[1]) != nil) != enabled || st.LessonsEnabled != enabled {
			t.Fatal("unexpected key policy")
		}
		err = st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
			_, err := labmemory.Read(ctx, tx, user, labmemory.Ref{Kind: "lesson", ID: id})
			if !enabled && errors.Is(err, labmemory.ErrNotFound) {
				return nil
			}
			if !enabled {
				t.Fatalf("disabled lesson read: %v", err)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
