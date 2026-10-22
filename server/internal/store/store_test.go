package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	observer, err := pgx.Connect(t.Context(), env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	var published []db.Event
	s := &Store{Pool: env.App, Publish: func(ctx context.Context, evs []db.Event) {
		for _, e := range evs {
			var typ string
			if err := observer.QueryRow(ctx, "SELECT type FROM event WHERE id=$1", e.ID).Scan(&typ); err != nil || typ != e.Type {
				t.Fatalf("publication before committed visibility: type=%q err=%v", typ, err)
			}
		}
		published = append(published, evs...)
	}}
	u, ws := seed(t, s)
	if len(published) != 1 || published[0].Type != "workspace.created" || published[0].WorkspaceID != ws {
		t.Fatalf("%+v", published)
	}
	err = s.InWorkspace(context.Background(), ws, u, func(tx *Tx) error {
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

// This uses Emit directly: handler workspace locks must not mask a missing
// event-insert lock and allow a higher id to commit before a lower one.
func TestDirectConcurrentEmitCommitsInCursorOrder(t *testing.T) {
	env := testutil.Setup(t)
	s := &Store{Pool: env.App}
	u, ws := seed(t, s)
	ready := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	released := false
	defer func() {
		if !released {
			close(release)
		}
		<-firstDone
	}()
	go func() {
		firstDone <- s.InWorkspace(t.Context(), ws, u, func(tx *Tx) error {
			if err := tx.Emit(t.Context(), "first", nil, System, nil); err != nil {
				return err
			}
			close(ready)
			<-release
			return nil
		})
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("first emit did not reach commit barrier")
	}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- s.InWorkspace(t.Context(), ws, u, func(tx *Tx) error { return tx.Emit(t.Context(), "second", nil, System, nil) })
	}()
	deadline := time.Now().Add(time.Second)
	for {
		select {
		case err := <-secondDone:
			t.Fatalf("second event committed before first: %v", err)
		default:
		}
		var blocked bool
		if err := env.Owner.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND usename='lykeion_app' AND wait_event='advisory')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second emit did not wait on workspace event lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var rows []db.Event
	read := func(after int64) error {
		return s.InWorkspace(t.Context(), ws, u, func(tx *Tx) error {
			var err error
			rows, err = tx.Q.EventsAfter(t.Context(), db.EventsAfterParams{WorkspaceID: ws, After: after, Lim: 500})
			return err
		})
	}
	if err := read(0); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("uncommitted events visible: %+v", rows)
	}
	close(release)
	released = true
	// Leave firstDone for deferred cleanup; its transaction has to commit before
	// the second waiter can finish, regardless of callback scheduling.
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second emit stayed blocked")
	}
	if err := read(rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Type != "first" || rows[1].Type != "second" || rows[0].ID >= rows[1].ID {
		t.Fatalf("cursor order: %+v", rows)
	}
	if err := read(rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Type != "second" {
		t.Fatalf("resuming cursor missed second event: %+v", rows)
	}
}

func TestAfterCommitRunsOnlyWhenTheTransactionCommits(t *testing.T) {
	env := testutil.Setup(t)
	s := &Store{Pool: env.App}
	user, ws := seed(t, s)
	ctx := context.Background()
	ran := 0
	if err := s.InWorkspace(ctx, ws, user, func(tx *Tx) error {
		tx.AfterCommit(func() { ran++ })
		return errors.New("rolled back")
	}); err == nil {
		t.Fatal("the failing transaction reported success")
	}
	if ran != 0 {
		t.Fatalf("hook ran %d times on rollback", ran)
	}
	if err := s.InWorkspace(ctx, ws, user, func(tx *Tx) error {
		tx.AfterCommit(func() { ran++ })
		return nil
	}); err != nil || ran != 1 {
		t.Fatalf("commit: err %v, ran %d", err, ran)
	}
}

func TestAfterCommitInASavepointRunsWithItsTransaction(t *testing.T) {
	env := testutil.Setup(t)
	s := &Store{Pool: env.App}
	user, ws := seed(t, s)
	ctx := context.Background()
	var ran []string
	if err := s.InWorkspace(ctx, ws, user, func(tx *Tx) error {
		_ = tx.Savepoint(ctx, func(sp *Tx) error {
			sp.AfterCommit(func() { ran = append(ran, "undone") })
			return errors.New("undone")
		})
		if err := tx.Savepoint(ctx, func(sp *Tx) error {
			sp.AfterCommit(func() { ran = append(ran, "kept") })
			return nil
		}); err != nil {
			return err
		}
		if len(ran) != 0 {
			t.Error("a savepoint's hook ran before its transaction committed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "kept" {
		t.Fatalf("hooks ran %v, want only the kept savepoint's", ran)
	}
	ran = nil
	_ = s.InWorkspace(ctx, ws, user, func(tx *Tx) error {
		_ = tx.Savepoint(ctx, func(sp *Tx) error {
			sp.AfterCommit(func() { ran = append(ran, "rolled back") })
			return nil
		})
		return errors.New("rolled back")
	})
	if len(ran) != 0 {
		t.Fatalf("a rolled-back transaction ran %v", ran)
	}
}
