package autoresearch

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type fix struct {
	env   *testutil.Env
	s     *Service
	ws    uuid.UUID
	owner uuid.UUID
	now   time.Time
}

func setup(t *testing.T) *fix {
	t.Helper()
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	f := &fix{env: env, ws: uuid.New(), now: time.Now().UTC().Truncate(time.Second)}
	f.s = &Service{Store: st, AgentRuns: &service.AgentRuns{Store: st}, Now: func() time.Time { return f.now }}
	f.exec(t, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'w', 'w')`, f.ws)
	f.owner = f.user(t, "a@lab.org", "owner")
	// Every workspace holds the built-in stage Teams.
	if err := st.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		return pack.InstallShipped(context.Background(), tx, storage.Local{Dir: t.TempDir()}, testutil.BuiltIns(t))
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// builtIn is the workspace's built-in Team for a stage.
func (f *fix) builtIn(t *testing.T, stage string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.env.Owner.QueryRow(context.Background(), `SELECT id FROM field_pack WHERE workspace_id = $1 AND source = 'shipped' AND stage = $2`,
		f.ws, stage).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// cardTeams is each column's Team on a study.
func (f *fix) cardTeams(t *testing.T, card uuid.UUID) map[string]uuid.UUID {
	t.Helper()
	rows, err := f.env.Owner.Query(context.Background(), `SELECT board_column, pack_id FROM card_team WHERE card_id = $1`, card)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var col string
		var id uuid.UUID
		if err := rows.Scan(&col, &id); err != nil {
			t.Fatal(err)
		}
		out[col] = id
	}
	return out
}

// user creates a user and, unless role is "", makes them a member.
func (f *fix) user(t *testing.T, email, role string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	u, err := f.s.Store.Global().UpsertUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	if role != "" {
		f.exec(t, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)`, f.ws, u.ID, role)
	}
	return u.ID
}

func (f *fix) autoresearch(t *testing.T, mode, title, description, template string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	tx, err := f.env.Owner.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := db.New(tx).LockWorkspaceEvents(context.Background(), f.ws); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `INSERT INTO autoresearch (workspace_id, owner_id, title, description, execution_mode, title_template, scope_to)
		VALUES ($1, $2, $3, $4, $5, $6, 'validation') RETURNING id`, f.ws, f.owner, title, description, mode, template).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return id
}

// fire admits a run the way a trigger would, filling in what the test leaves
// out, and dispatches it.
func (f *fix) fire(t *testing.T, ap uuid.UUID, a Admission) db.AutoresearchRun {
	t.Helper()
	ctx := context.Background()
	a.Autoresearch = ap
	if a.Source == "" {
		a.Source = "manual"
	}
	if a.Actor == uuid.Nil {
		a.Actor = f.owner
	}
	if a.Key == "" {
		a.Key = "test:" + uuid.NewString()
	}
	if a.PlannedAt.IsZero() {
		a.PlannedAt = f.now
	}
	var r db.AutoresearchRun
	err := f.s.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		r, _, err = f.s.Admit(ctx, tx, a)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Dispatch(ctx, f.ws, r.ID); err != nil {
		t.Fatal(err)
	}
	return f.run(t, r.ID)
}

func (f *fix) run(t *testing.T, id uuid.UUID) db.AutoresearchRun {
	t.Helper()
	var r db.AutoresearchRun
	err := f.s.Store.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		r, err = tx.Q.GetRun(context.Background(), db.GetRunParams{WorkspaceID: f.ws, ID: id})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *fix) card(t *testing.T, id uuid.UUID) db.Card {
	t.Helper()
	var c db.Card
	err := f.s.Store.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		c, err = tx.Q.GetCard(context.Background(), db.GetCardParams{WorkspaceID: f.ws, ID: id})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fix) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.env.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func reason(r db.AutoresearchRun) string { return r.Status + "/" + r.Reason }

func (f *fix) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := db.New(tx).LockWorkspaceEvents(ctx, f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
