package team

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type shippedLab struct {
	*lab
	env   *testutil.Env
	owner uuid.UUID
}

// newShippedLab is a workspace with the shipped Teams installed and no agents.
func newShippedLab(t *testing.T) *shippedLab {
	t.Helper()
	ctx := context.Background()
	env := testutil.Setup(t)
	l := &shippedLab{lab: &lab{store: &store.Store{Pool: env.App}, ws: uuid.New()}, env: env}
	owner, err := l.store.Global().UpsertUserByEmail(ctx, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	l.owner = owner.ID
	err = l.store.InWorkspace(ctx, l.ws, owner.ID, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: l.ws, Slug: "lab", Name: "Lab"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: l.ws, UserID: owner.ID, Role: "owner"}); err != nil {
			return err
		}
		return pack.InstallShipped(ctx, tx, storage.Local{Dir: t.TempDir()}, pack.Shipped())
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *shippedLab) do(t *testing.T, user uuid.UUID, fn func(context.Context, *store.Tx) error) {
	t.Helper()
	if err := l.store.InWorkspace(context.Background(), l.ws, user, func(tx *store.Tx) error { return fn(context.Background(), tx) }); err != nil {
		t.Fatal(err)
	}
}

func (l *shippedLab) install(t *testing.T) {
	t.Helper()
	l.do(t, uuid.Nil, func(ctx context.Context, tx *store.Tx) error { return InstallLaboratory(ctx, tx) })
}

// stagePacks is each built-in stage Team with its manifest.
func (l *shippedLab) stagePacks(t *testing.T) map[uuid.UUID]pack.Manifest {
	t.Helper()
	out := map[uuid.UUID]pack.Manifest{}
	l.do(t, uuid.Nil, func(ctx context.Context, tx *store.Tx) error {
		for _, s := range board.Stages {
			p, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: tx.WS, Stage: s})
			if err != nil {
				return err
			}
			var m pack.Manifest
			if err := json.Unmarshal(p.Manifest, &m); err != nil {
				return err
			}
			out[p.ID] = m
		}
		return nil
	})
	return out
}

// addCopies gives user their private copies of every stage Team the old way.
func (l *shippedLab) addCopies(t *testing.T, user uuid.UUID, daemon string) {
	t.Helper()
	l.do(t, user, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.Q.UpsertRuntime(ctx, db.UpsertRuntimeParams{WorkspaceID: tx.WS, OwnerID: user, DaemonID: daemon, Harness: "claude", DeviceName: daemon}); err != nil {
			return err
		}
		for _, s := range board.Stages {
			p, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: tx.WS, Stage: s})
			if err != nil {
				return err
			}
			var m pack.Manifest
			if err := json.Unmarshal(p.Manifest, &m); err != nil {
				return err
			}
			if _, err := Add(ctx, tx, p, m, user); err != nil {
				return err
			}
		}
		return nil
	})
}

type agentRow struct {
	ID                           uuid.UUID
	Name, Visibility, Stage, Lab string
	Instructions, Model, Harness string
	Owner                        uuid.UUID
	Deleted                      bool
}

func (l *shippedLab) allAgents(t *testing.T) []agentRow {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(), `SELECT id, name, visibility, stage, laboratory, instructions, model, harness, owner_id, deleted_at IS NOT NULL
FROM agent WHERE workspace_id = $1 ORDER BY created_at, id`, l.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []agentRow
	for rows.Next() {
		var a agentRow
		if err := rows.Scan(&a.ID, &a.Name, &a.Visibility, &a.Stage, &a.Lab, &a.Instructions, &a.Model, &a.Harness, &a.Owner, &a.Deleted); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func (l *shippedLab) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := l.env.Owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// wantLaboratory checks the 18 shared agents against the shipped manifests.
func (l *shippedLab) wantLaboratory(t *testing.T) map[string]agentRow {
	t.Helper()
	byName := map[string]agentRow{}
	for _, a := range l.allAgents(t) {
		if !a.Deleted {
			byName[a.Name] = a
		}
	}
	want := 0
	for id, m := range l.stagePacks(t) {
		var p db.FieldPack
		l.do(t, uuid.Nil, func(ctx context.Context, tx *store.Tx) (err error) {
			p, err = tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: tx.WS, ID: id})
			return
		})
		for _, mem := range slices.Concat([]pack.Member{m.Leader, m.Auditor}, m.Members) {
			want++
			a, ok := byName[mem.Name+" · "+m.Name]
			if !ok {
				t.Fatalf("no live agent %q among %v", mem.Name+" · "+m.Name, keys(byName))
			}
			harnesses := []string{}
			for h := range mem.Agent.Models {
				harnesses = append(harnesses, h)
			}
			sort.Strings(harnesses)
			wantModel := mem.Agent.Model
			if len(harnesses) > 0 {
				wantModel = mem.Agent.Models[harnesses[0]]
			}
			if a.Lab != "built-in" || a.Visibility != "workspace" || a.Stage != p.Stage || a.Owner != l.owner ||
				a.Instructions != mem.Agent.Instructions || a.Model != wantModel {
				t.Errorf("agent %q: lab=%q vis=%q stage=%q (want %q) owner=%v model=%q (want %q) instructions match=%v",
					a.Name, a.Lab, a.Visibility, a.Stage, p.Stage, a.Owner == l.owner, a.Model, wantModel, a.Instructions == mem.Agent.Instructions)
			}
			if n := l.count(t, `SELECT count(*) FROM team_agent WHERE workspace_id = $1 AND pack_id = $2 AND member = $3 AND user_id IS NULL AND agent_id = $4`,
				l.ws, id, mem.Name, a.ID); n != 1 {
				t.Errorf("%q has %d shared rows", a.Name, n)
			}
		}
	}
	if want != 18 {
		t.Fatalf("the shipped laboratory has %d members, want 18", want)
	}
	if n := l.count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND deleted_at IS NULL`, l.ws); n != 18 {
		t.Fatalf("%d live agents, want 18", n)
	}
	if n := l.count(t, `SELECT count(*) FROM team_agent WHERE workspace_id = $1 AND user_id IS NULL`, l.ws); n != 18 {
		t.Fatalf("%d shared team rows, want 18", n)
	}
	if n := l.count(t, `SELECT count(*) FROM team_agent WHERE workspace_id = $1 AND user_id IS NOT NULL`, l.ws); n != 0 {
		t.Fatalf("%d private team rows remain", n)
	}
	return byName
}

func keys(m map[string]agentRow) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestInstallLaboratoryCreatesSharedAgents(t *testing.T) {
	l := newShippedLab(t)
	l.install(t)
	l.wantLaboratory(t)
	if n := l.count(t, `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'laboratory.installed' AND payload->'reset' = '[]'::jsonb`, l.ws); n != 1 {
		t.Fatalf("%d laboratory.installed events with an empty reset list", n)
	}
}

func TestInstallLaboratoryIsIdempotent(t *testing.T) {
	l := newShippedLab(t)
	l.install(t)
	before := l.allAgents(t)
	events := l.count(t, `SELECT count(*) FROM event WHERE workspace_id = $1`, l.ws)
	l.install(t)
	if after := l.allAgents(t); !slices.Equal(before, after) {
		t.Fatalf("a second install changed the agents:\n%v\n%v", before, after)
	}
	if n := l.count(t, `SELECT count(*) FROM event WHERE workspace_id = $1`, l.ws); n != events {
		t.Fatalf("a second install emitted %d events", n-events)
	}
	l.wantLaboratory(t)
}

func TestInstallLaboratoryMergesPrivateCopies(t *testing.T) {
	l := newShippedLab(t)
	ben := l.user(t, "ben@lab.org")
	l.addCopies(t, l.owner, "d-owner")
	l.addCopies(t, ben, "d-ben")
	copies := l.allAgents(t)
	if len(copies) != 36 {
		t.Fatalf("%d copies, want 36", len(copies))
	}
	// The owner's copies are older; Ben's carry the suffixed names.
	var leaderName string
	for _, m := range l.stagePacks(t) {
		if m.Stage == board.Grounding {
			leaderName = m.Leader.Name + " · " + m.Name
		}
	}
	oldest, newer := agentRow{}, agentRow{}
	for _, a := range copies {
		if a.Name == leaderName {
			oldest = a
		}
		if a.Name == leaderName+" 2" {
			newer = a
		}
	}
	if oldest.ID == uuid.Nil || newer.ID == uuid.Nil || oldest.Owner != l.owner || newer.Owner != ben {
		t.Fatalf("leader copies: %+v %+v", oldest, newer)
	}
	// The owner customised their copy; each person has a study and a run on theirs.
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := l.env.Owner.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE agent SET instructions = 'my own way', name = 'My leader' WHERE id = $1`, oldest.ID)
	// Ben customised his too, and holds a private Coresearch conversation and
	// leads a project with it.
	exec(`UPDATE agent SET instructions = 'ben''s way' WHERE id = $1`, newer.ID)
	var project, chat, conversation, research uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), `INSERT INTO coresearch_project (workspace_id, name, created_by) VALUES ($1, 'p', $2) RETURNING id`, l.ws, ben).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := l.env.Owner.QueryRow(context.Background(), `INSERT INTO coresearch_chat (workspace_id, project_id, owner_id, mode, cli) VALUES ($1, $2, $3, 'experiment', 'claude') RETURNING id`, l.ws, project, ben).Scan(&chat); err != nil {
		t.Fatal(err)
	}
	if err := l.env.Owner.QueryRow(context.Background(), `INSERT INTO coresearch_conversation (workspace_id, owner_id, agent_id, project_id, chat_id) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		l.ws, ben, newer.ID, project, chat).Scan(&conversation); err != nil {
		t.Fatal(err)
	}
	if err := l.env.Owner.QueryRow(context.Background(), `INSERT INTO research (workspace_id, name, created_by, lead_type, lead_id) VALUES ($1, 'r', $2, 'agent', $3) RETURNING id`,
		l.ws, ben, newer.ID).Scan(&research); err != nil {
		t.Fatal(err)
	}
	runs := map[uuid.UUID]uuid.UUID{}
	for _, c := range []agentRow{oldest, newer} {
		l.do(t, c.Owner, func(ctx context.Context, tx *store.Tx) error {
			n, err := tx.Q.NextCardNumber(ctx, tx.WS)
			if err != nil {
				return err
			}
			typ := "agent"
			card, err := tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: tx.WS, Number: n, Title: "q", Question: "q", OwnerID: c.Owner,
				ScopeFrom: board.Grounding, ScopeTo: board.Grounding, BoardColumn: board.Grounding, Round: 1, AssigneeType: &typ, AssigneeID: &c.ID})
			if err != nil {
				return err
			}
			r, err := tx.Q.EnqueueAgentRun(ctx, db.EnqueueAgentRunParams{WorkspaceID: tx.WS, CardID: &card.ID, ResponsibleUserID: c.Owner,
				BoardColumn: board.Grounding, Round: 1, Role: "leader", Harness: "claude", MaxAttempts: 1, AgentID: &c.ID})
			runs[r.ID] = c.Owner
			return err
		})
	}

	l.install(t)
	byName := l.wantLaboratory(t)
	kept := byName[leaderName]
	if kept.ID != oldest.ID {
		t.Fatalf("kept %v, want the oldest copy %v", kept.ID, oldest.ID)
	}
	live := map[uuid.UUID]bool{}
	for _, a := range byName {
		live[a.ID] = true
	}
	for _, c := range copies {
		if !live[c.ID] && l.count(t, `SELECT count(*) FROM agent WHERE id = $1 AND deleted_at IS NOT NULL`, c.ID) != 1 {
			t.Errorf("copy %q was not retired", c.Name)
		}
	}
	// Nothing references a retired copy. The list is kept by hand so that a
	// column the merge misses fails here; a new foreign key must join it.
	cols := map[[2]string]bool{
		{"agent_run", "agent_id"}: true, {"coresearch_conversation", "agent_id"}: true, {"team_agent", "agent_id"}: true,
		{"agent_capabilities", "agent_id"}: true, {"card", "assignee_id"}: true, {"autoresearch", "assignee_id"}: true,
		{"research", "lead_id"}: true,
	}
	refs, err := l.env.Owner.Query(context.Background(), `SELECT c.conrelid::regclass::text, a.attname
FROM pg_constraint c CROSS JOIN LATERAL unnest(c.conkey, c.confkey) k(col, ref)
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.col
JOIN pg_attribute r ON r.attrelid = c.confrelid AND r.attnum = k.ref
WHERE c.contype = 'f' AND c.confrelid = 'agent'::regclass AND r.attname = 'id'`)
	if err != nil {
		t.Fatal(err)
	}
	for refs.Next() {
		var tbl, col string
		if err := refs.Scan(&tbl, &col); err != nil {
			t.Fatal(err)
		}
		if !cols[[2]string{tbl, col}] {
			t.Errorf("%s.%s references agent and is missing from this test's list", tbl, col)
		}
	}
	refs.Close()
	for c := range cols {
		if n := l.count(t, `SELECT count(*) FROM `+c[0]+` x JOIN agent a ON a.id = x.`+c[1]+` WHERE a.workspace_id = $1 AND a.deleted_at IS NOT NULL`, l.ws); n != 0 {
			t.Errorf("%s.%s: %d rows reference a retired copy", c[0], c[1], n)
		}
	}
	// Ben, through the app role and its per-owner policy, finds his
	// conversation on the shared agent.
	var seen uuid.UUID
	appTx, err := l.env.App.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer appTx.Rollback(context.Background())
	if _, err := appTx.Exec(context.Background(), `SELECT set_config('app.workspace_id', $1, true), set_config('app.user_id', $2, true)`, l.ws.String(), ben.String()); err != nil {
		t.Fatal(err)
	}
	if err := appTx.QueryRow(context.Background(), `SELECT agent_id FROM coresearch_conversation WHERE id = $1`, conversation).Scan(&seen); err != nil || seen != kept.ID {
		t.Errorf("Ben's conversation is on %v (%v), want the shared %v", seen, err, kept.ID)
	}
	if n := l.count(t, `SELECT count(*) FROM research WHERE id = $1 AND lead_type = 'agent' AND lead_id = $2`, research, kept.ID); n != 1 {
		t.Error("the project's lead was not moved onto the shared agent")
	}
	for run, owner := range runs {
		if n := l.count(t, `SELECT count(*) FROM agent_run WHERE id = $1 AND agent_id = $2 AND responsible_user_id = $3`, run, kept.ID, owner); n != 1 {
			t.Errorf("run %v was not moved onto the kept agent with its owner", run)
		}
	}
	if n := l.count(t, `SELECT count(*) FROM card WHERE workspace_id = $1 AND assignee_id = $2`, l.ws, kept.ID); n != 2 {
		t.Errorf("%d studies on the kept agent, want 2", n)
	}
	var reset []string
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT payload->'reset' FROM event WHERE workspace_id = $1 AND type = 'laboratory.installed'`, l.ws).Scan(&reset); err != nil {
		t.Fatal(err)
	}
	sort.Strings(reset)
	if want := []string{"My leader", leaderName + " 2"}; !slices.Equal(reset, want) {
		t.Fatalf("reset %v, want %v", reset, want)
	}
}

func TestEnsureShippedTeamMakesNoCopies(t *testing.T) {
	l := newShippedLab(t)
	ben := l.user(t, "ben@lab.org")
	l.install(t)
	before := l.count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1`, l.ws)
	for id := range l.stagePacks(t) {
		l.do(t, ben, func(ctx context.Context, tx *store.Tx) error { return Ensure(ctx, tx, ben, id) })
	}
	if n := l.count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1`, l.ws); n != before {
		t.Fatalf("Ensure made %d agents", n-before)
	}
}

func TestEnsureShippedTeamInstallsTheLaboratory(t *testing.T) {
	l := newShippedLab(t)
	for id := range l.stagePacks(t) {
		l.do(t, l.owner, func(ctx context.Context, tx *store.Tx) error { return Ensure(ctx, tx, l.owner, id) })
		break
	}
	l.wantLaboratory(t)
}

func TestOwnerLeavingKeepsLaboratory(t *testing.T) {
	l := newShippedLab(t)
	l.install(t)
	var own db.Agent
	l.do(t, l.owner, func(ctx context.Context, tx *store.Tx) (err error) {
		own, err = tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: tx.WS, Name: "mine", Harness: "claude", Visibility: "private", OwnerID: l.owner})
		return
	})
	var archived []db.Agent
	l.do(t, l.owner, func(ctx context.Context, tx *store.Tx) (err error) {
		archived, err = tx.Q.ArchiveOwnedAgents(ctx, db.ArchiveOwnedAgentsParams{WorkspaceID: tx.WS, OwnerID: l.owner})
		return
	})
	if len(archived) != 1 || archived[0].ID != own.ID {
		t.Fatalf("archived %d agents, want only the owner's own", len(archived))
	}
	if n := l.count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND laboratory <> '' AND archived_at IS NULL`, l.ws); n != 18 {
		t.Fatalf("%d live laboratory agents after the owner left, want 18", n)
	}
}

func TestInstallLaboratoryGivesEachNewAgentAnIconFromTheSet(t *testing.T) {
	l := newShippedLab(t)
	l.install(t)
	n := 0
	for _, a := range l.agents(t) {
		if a.Laboratory == "" {
			continue
		}
		n++
		if g, ok := strings.CutPrefix(a.AvatarUrl, "emoji:"); !ok || !slices.Contains(builtInEmoji, g) {
			t.Errorf("%s has icon %q", a.Name, a.AvatarUrl)
		}
	}
	if n == 0 {
		t.Fatal("no laboratory agents installed")
	}
}

func TestInstallLaboratoryGivesAConvertedCopyAnIcon(t *testing.T) {
	l := newShippedLab(t)
	l.addCopies(t, l.owner, "d-owner")
	l.install(t)
	n := 0
	for _, a := range l.agents(t) {
		if a.Laboratory == "" {
			continue
		}
		n++
		if g, ok := strings.CutPrefix(a.AvatarUrl, "emoji:"); !ok || !slices.Contains(builtInEmoji, g) {
			t.Errorf("%s has icon %q", a.Name, a.AvatarUrl)
		}
	}
	if n == 0 {
		t.Fatal("no laboratory agents installed")
	}
}
