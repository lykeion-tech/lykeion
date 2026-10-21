package team

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func m(name, harness, model string) pack.Member {
	return pack.Member{Name: name, Agent: pack.AgentDef{Harness: harness, Model: model}}
}

func TestAuditorMustDifferFromEveryMember(t *testing.T) {
	for _, c := range []struct {
		name                    string
		leader, auditor, member pack.Member
		refused                 bool
	}{
		{"same harness, both default models", m("pi", "codex", ""), m("a", "claude", ""), m("x", "claude", ""), true},
		{"same harness, different models", m("pi", "codex", ""), m("a", "claude", "opus"), m("x", "claude", "sonnet"), false},
		{"different harness", m("pi", "claude", ""), m("a", "codex", ""), m("x", "claude", ""), false},
		{"equal to the leader", m("pi", "claude", "opus"), m("a", "claude", "opus"), m("x", "codex", ""), true},
	} {
		err := AuditorDiffers(pack.Manifest{Leader: c.leader, Auditor: c.auditor, Members: []pack.Member{c.member}})
		if (err != nil) != c.refused {
			t.Errorf("%s: err = %v, refused should be %v", c.name, err, c.refused)
		}
	}
}

type lab struct {
	store *store.Store
	ws    uuid.UUID
	pack  db.FieldPack
	man   pack.Manifest
}

func (l *lab) user(t *testing.T, email string) uuid.UUID {
	t.Helper()
	u, err := l.store.Global().UpsertUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	err = l.store.InWorkspace(context.Background(), l.ws, u.ID, func(tx *store.Tx) error {
		return tx.Q.AddMember(context.Background(), db.AddMemberParams{WorkspaceID: l.ws, UserID: u.ID, Role: "member"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func newLab(t *testing.T) (*lab, uuid.UUID) {
	t.Helper()
	env := testutil.Setup(t)
	l := &lab{store: &store.Store{Pool: env.App}, ws: uuid.New()}
	ctx := context.Background()
	owner, err := l.store.Global().UpsertUserByEmail(ctx, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	err = l.store.InWorkspace(ctx, l.ws, owner.ID, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: l.ws, Slug: "lab", Name: "Lab"}); err != nil {
			return err
		}
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: l.ws, UserID: owner.ID, Role: "owner"})
	})
	if err != nil {
		t.Fatal(err)
	}
	z := testutil.Pack(t, testutil.PackOpts{Members: 2})
	p, err := pack.Parse(z)
	if err != nil {
		t.Fatal(err)
	}
	l.man = p.Manifest
	err = l.store.InWorkspace(ctx, l.ws, owner.ID, func(tx *store.Tx) error {
		l.pack, err = tx.Q.InsertFieldPack(ctx, db.InsertFieldPackParams{WorkspaceID: l.ws, Name: p.Manifest.Name,
			Version: p.Manifest.Version, Sha256: p.SHA256, Manifest: []byte("{}"), BlobKey: "k", Source: "shipped"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return l, owner.ID
}

func (l *lab) add(t *testing.T, user uuid.UUID) []db.TeamAgent {
	t.Helper()
	var out []db.TeamAgent
	err := l.store.InWorkspace(context.Background(), l.ws, user, func(tx *store.Tx) (err error) {
		out, err = Add(context.Background(), tx, l.pack, l.man, user)
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (l *lab) agents(t *testing.T) []db.Agent {
	t.Helper()
	var out []db.Agent
	err := l.store.InWorkspace(context.Background(), l.ws, uuid.Nil, func(tx *store.Tx) (err error) {
		out, err = tx.Q.ListAgents(context.Background(), db.ListAgentsParams{WorkspaceID: l.ws, UserID: uuid.Nil, IsAdmin: true})
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAddCreatesPrivateAgentsOwnedByTheAdder(t *testing.T) {
	l, ana := newLab(t)
	ben := l.user(t, "ben@lab.org")
	for _, u := range []uuid.UUID{ana, ben} {
		if rows := l.add(t, u); len(rows) != 4 {
			t.Fatalf("%d team agents, want 4", len(rows))
		}
	}
	agents := l.agents(t)
	if len(agents) != 8 {
		t.Fatalf("%d agents, want 8", len(agents))
	}
	owned := map[uuid.UUID]int{}
	for _, a := range agents {
		if a.Visibility != "private" {
			t.Errorf("%s is %s", a.Name, a.Visibility)
		}
		owned[a.OwnerID]++
	}
	if owned[ana] != 4 || owned[ben] != 4 {
		t.Fatalf("owners %v", owned)
	}
}

func TestAddNamesAroundClashes(t *testing.T) {
	l, ana := newLab(t)
	ctx := context.Background()
	err := l.store.InWorkspace(ctx, l.ws, ana, func(tx *store.Tx) error {
		_, err := tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: l.ws, Name: "pi · demo", Harness: "claude", Visibility: "workspace", OwnerID: ana})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	l.add(t, ana)
	found := false
	for _, a := range l.agents(t) {
		found = found || a.Name == "pi · demo 2"
	}
	if !found {
		t.Fatalf("no agent named %q in %v", "pi · demo 2", l.agents(t))
	}
}

func TestAddNamesTheMembersByTheirOwnName(t *testing.T) {
	l, ana := newLab(t)
	l.add(t, ana)
	want := map[string]bool{"pi · demo": true, "auditor · demo": true, "member-1 · demo": true, "member-2 · demo": true}
	for _, a := range l.agents(t) {
		delete(want, a.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing agents %v", want)
	}
}

func TestAddTwiceChangesNothing(t *testing.T) {
	l, ana := newLab(t)
	first, second := l.add(t, ana), l.add(t, ana)
	if len(first) != 4 || len(second) != 4 || len(l.agents(t)) != 4 {
		t.Fatalf("first %d, second %d, agents %d", len(first), len(second), len(l.agents(t)))
	}
}

func TestAddPicksTheOwnersHarness(t *testing.T) {
	for _, c := range []struct {
		name    string
		have    []string
		harness string
		model   string
		err     error
	}{
		{"codex only", []string{"codex"}, "codex", "gpt-6", nil},
		{"claude only", []string{"claude"}, "claude", "opus", nil},
		{"both", []string{"codex", "claude"}, "claude", "opus", nil},
		{"none", nil, "", "", ErrNoHarness},
	} {
		l, owner := newLab(t)
		ctx := context.Background()
		for i, h := range c.have {
			err := l.store.InWorkspace(ctx, l.ws, owner, func(tx *store.Tx) error {
				// Offline runtimes count.
				_, err := tx.Q.UpsertRuntime(ctx, db.UpsertRuntimeParams{WorkspaceID: l.ws, OwnerID: owner,
					DaemonID: uuid.NewString() + string(rune('a'+i)), Harness: h})
				if err != nil {
					return err
				}
				_, err = tx.Q.SweepWorkspaceRuntimes(ctx, db.SweepWorkspaceRuntimesParams{WorkspaceID: l.ws,
					Stale: pgtype.Interval{Microseconds: -1_000_000, Valid: true}})
				return err
			})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
		}
		l.man.Members[0].Agent = pack.AgentDef{Models: map[string]string{"claude": "opus", "codex": "gpt-6"}, Instructions: "i"}
		err := l.store.InWorkspace(ctx, l.ws, owner, func(tx *store.Tx) error {
			_, err := Add(ctx, tx, l.pack, l.man, owner)
			return err
		})
		if !errors.Is(err, c.err) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.err)
		}
		if c.err != nil {
			continue
		}
		found := false
		for _, a := range l.agents(t) {
			if a.Name == l.man.Members[0].Name+" · "+l.man.Name {
				found = true
				if a.Harness != c.harness || a.Model != c.model {
					t.Errorf("%s: agent %s/%s, want %s/%s", c.name, a.Harness, a.Model, c.harness, c.model)
				}
			}
		}
		if !found {
			t.Errorf("%s: member agent not created", c.name)
		}
	}
}
