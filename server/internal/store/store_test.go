package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func seed(t *testing.T, s *Store) (user uuid.UUID, ws uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	u, err := s.Global().UpsertUserByEmail(ctx, "a@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	ws = uuid.New()
	err = s.InWorkspace(ctx, ws, u.ID, func(tx *Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: ws, Slug: "lab-" + ws.String()[:8], Name: "Lab"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws, UserID: u.ID, Role: "owner"}); err != nil {
			return err
		}
		return tx.Emit(ctx, "workspace.created", nil, User(u.ID), map[string]string{"name": "Lab"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID, ws
}

func TestEmitPublishesAfterCommitOnly(t *testing.T) {
	env := testutil.Setup(t)
	var published []db.Event
	s := &Store{Pool: env.App, Publish: func(_ context.Context, evs []db.Event) { published = append(published, evs...) }}
	u, ws := seed(t, s)
	if len(published) != 1 || published[0].Type != "workspace.created" || published[0].WorkspaceID != ws {
		t.Fatalf("%+v", published)
	}
	err := s.InWorkspace(context.Background(), ws, u, func(tx *Tx) error {
		if err := tx.Emit(context.Background(), "x", nil, System, nil); err != nil {
			return err
		}
		return errors.New("roll back")
	})
	if err == nil || len(published) != 1 {
		t.Fatal("a rolled-back event was published")
	}
}

func TestSettingsDoNotLeakAcrossTransactions(t *testing.T) {
	env := testutil.Setup(t)
	s := &Store{Pool: env.App}
	seed(t, s)
	for i := 0; i < 5; i++ {
		var n int
		if err := env.App.QueryRow(context.Background(), "SELECT count(*) FROM workspace").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("a query without settings saw %d workspaces", n)
		}
	}
}

func TestAsUserSeesOnlyOwnWorkspaces(t *testing.T) {
	env := testutil.Setup(t)
	s := &Store{Pool: env.App}
	u, _ := seed(t, s)
	_, _ = seed(t, s)
	var rows []db.ListMyWorkspacesRow
	if err := s.AsUser(context.Background(), u, "a@lab.org", func(tx *Tx) error {
		var err error
		rows, err = tx.Q.ListMyWorkspaces(context.Background(), u)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 workspaces for this user, got %d", len(rows))
	}
	stranger := uuid.New()
	if err := s.AsUser(context.Background(), stranger, "s@x", func(tx *Tx) error {
		var err error
		rows, err = tx.Q.ListMyWorkspaces(context.Background(), stranger)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatal("a stranger listed workspaces")
	}
}
