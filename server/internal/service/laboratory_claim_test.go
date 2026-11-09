package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// labFixture is a workspace with the built-in laboratory installed, its owner
// (f.Owner), a member who owns a study, and the Grounding Team's leader.
type labFixture struct {
	*fixture
	member uuid.UUID
	study  uuid.UUID
	leader db.Agent
}

func newLabFixture(t *testing.T) *labFixture {
	t.Helper()
	f := setup(t)
	ctx := context.Background()
	m, err := f.T.Store.Global().UpsertUserByEmail(ctx, "member@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	l := &labFixture{fixture: f, member: m.ID}
	if err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: m.ID, Role: "member"}); err != nil {
			return err
		}
		return pack.InstallShipped(ctx, tx, storage.Local{Dir: t.TempDir()}, pack.Shipped())
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Store.InWorkspace(ctx, f.WS, uuid.Nil, func(tx *store.Tx) error { return team.InstallLaboratory(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Store.InWorkspace(ctx, f.WS, m.ID, func(tx *store.Tx) error {
		c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: board.Grounding, WorkspaceID: f.WS, Number: 2, Title: "m", Question: "q", OwnerID: m.ID})
		l.study = c.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	l.leader = l.agent(t)
	if l.leader.Laboratory == "" {
		t.Fatal("no laboratory leader")
	}
	return l
}

// agent reads the Grounding Team's shared leader as it now stands.
func (l *labFixture) agent(t *testing.T) db.Agent {
	t.Helper()
	var id uuid.UUID
	if err := l.Env.Owner.QueryRow(t.Context(), `SELECT t.agent_id FROM team_agent t JOIN field_pack p ON p.id = t.pack_id
		WHERE t.workspace_id = $1 AND t.user_id IS NULL AND t.member = 'leader' AND p.source = 'shipped' AND p.stage = 'grounding'`, l.WS).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var a db.Agent
	if err := l.T.Store.InWorkspace(t.Context(), l.WS, l.Owner, func(tx *store.Tx) (err error) {
		a, err = tx.Q.GetAgent(t.Context(), db.GetAgentParams{WorkspaceID: l.WS, ID: id})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

// register brings one runtime online for user on daemon.
func (l *labFixture) register(t *testing.T, user uuid.UUID, daemon, harness, os string) RuntimeRef {
	t.Helper()
	rs, err := l.T.Register(t.Context(), user, l.WS, daemon, daemon, []string{harness}, nil, agent.Platform{OS: os})
	if err != nil || len(rs) != 1 {
		t.Fatalf("register %s: %v", daemon, err)
	}
	return RuntimeRef{WorkspaceID: l.WS, RuntimeID: rs[0].ID, Attempt: 1}
}

// bind points the leader at a runtime, as the workspace owner would.
func (l *labFixture) bind(t *testing.T, ref RuntimeRef) {
	t.Helper()
	if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, l.leader.ID, ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
}

// run queues a role run of agent a on the member's study.
func (l *labFixture) run(t *testing.T, a uuid.UUID, harness string) db.AgentRun {
	t.Helper()
	var run db.AgentRun
	if err := l.T.Store.InWorkspace(t.Context(), l.WS, l.member, func(tx *store.Tx) (err error) {
		run, err = l.T.Enqueue(t.Context(), tx, l.member, NewAgentRun{Card: l.study, Column: board.Grounding, Round: 1, Role: "leader", Harness: harness, Prompt: "p", Agent: &a})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return run
}

func (l *labFixture) claims(t *testing.T, user uuid.UUID, ref RuntimeRef, options ...ClaimOptions) []Claimed {
	t.Helper()
	got, err := l.T.Claim(t.Context(), user, []RuntimeRef{ref}, 1, options...)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var guest = ClaimOptions{GuestIsolation: agent.GuestIsolationProtocol}

func TestBoundLabRunGoesOnlyToItsRuntime(t *testing.T) {
	l := newLabFixture(t)
	own := l.register(t, l.member, "member-own", "claude", "linux")
	other := l.register(t, l.Owner, "owner-other", "claude", "linux")
	bound := l.register(t, l.Owner, "owner-bound", "claude", "linux")
	l.bind(t, bound)
	for _, machine := range []*string{nil, ptr("member-own")} {
		if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE card SET machine = $2 WHERE id = $1`, l.study, machine); err != nil {
			t.Fatal(err)
		}
		run := l.run(t, l.leader.ID, "claude")
		if got := l.claims(t, l.member, own, guest); len(got) != 0 {
			t.Fatalf("machine %v: the study owner's own runtime claimed", machine)
		}
		if got := l.claims(t, l.Owner, other, guest); len(got) != 0 {
			t.Fatalf("machine %v: another runtime claimed", machine)
		}
		got := l.claims(t, l.Owner, bound, guest)
		if len(got) != 1 || got[0].AgentRun.ID != run.ID || got[0].AgentRun.ResponsibleUserID != l.member {
			t.Fatalf("machine %v: bound runtime claims %+v", machine, got)
		}
		if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status = 'cancelled' WHERE id = $1`, run.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func ptr(s string) *string { return &s }

func TestBoundLabRunOnColleagueMachineNeedsGuestIsolation(t *testing.T) {
	l := newLabFixture(t)
	mac := l.register(t, l.Owner, "owner-mac", "claude", "darwin")
	l.bind(t, mac)
	l.run(t, l.leader.ID, "claude")
	if got := l.claims(t, l.Owner, mac, guest); len(got) != 0 {
		t.Fatal("a colleague's Mac ran the study")
	}
	linux := l.register(t, l.Owner, "owner-linux", "claude", "linux")
	l.bind(t, linux)
	if got := l.claims(t, l.Owner, linux); len(got) != 0 {
		t.Fatal("a colleague's machine without guest isolation ran the study")
	}
	if got := l.claims(t, l.Owner, linux, guest); len(got) != 1 {
		t.Fatalf("guest-isolated Linux runtime claims %d", len(got))
	}
}

// A bound machine that stops allowing the agent's owner takes nothing more.
func TestBoundLabRunNeedsTheMachinesConsent(t *testing.T) {
	l := newLabFixture(t)
	shared := l.register(t, l.member, "member-shared", "claude", "linux")
	if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE runtime SET visibility = 'workspace' WHERE id = $1`, shared.RuntimeID); err != nil {
		t.Fatal(err)
	}
	l.bind(t, shared)
	l.run(t, l.leader.ID, "claude")
	if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE runtime SET visibility = 'private' WHERE id = $1`, shared.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if got := l.claims(t, l.member, shared, guest); len(got) != 0 {
		t.Fatal("a machine no longer shared with the agent's owner ran its study")
	}
	if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE runtime SET visibility = 'workspace' WHERE id = $1`, shared.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if got := l.claims(t, l.member, shared, guest); len(got) != 1 {
		t.Fatalf("shared again, claims %d", len(got))
	}
}

func TestUnboundLabRunWaits(t *testing.T) {
	l := newLabFixture(t)
	own := l.register(t, l.member, "member-own", "claude", "linux")
	shared := l.register(t, l.member, "member-shared", "claude", "linux")
	if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE runtime SET visibility = 'workspace' WHERE id = $1`, shared.RuntimeID); err != nil {
		t.Fatal(err)
	}
	l.run(t, l.leader.ID, "claude")
	l.run(t, l.leader.ID, "claude")
	for _, ref := range []RuntimeRef{own, shared} {
		if got := l.claims(t, l.member, ref, guest); len(got) != 0 {
			t.Fatal("an unbound laboratory run was claimed")
		}
	}
	var n int
	var text string
	if err := l.Env.Owner.QueryRow(t.Context(), `SELECT count(*), min(text) FROM inbox_item WHERE workspace_id = $1 AND user_id = $2
		AND kind = 'waiting_for_machine' AND resolved_at IS NULL`, l.WS, l.Owner).Scan(&n, &text); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(text, l.leader.Name) {
		t.Fatalf("owner's notices: %d %q", n, text)
	}
	var waiting *string
	if err := l.Env.Owner.QueryRow(t.Context(), `SELECT agent_run_waiting(workspace_id, id) FROM agent_run WHERE card_id = $1 LIMIT 1`, l.study).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if waiting == nil || !strings.Contains(*waiting, l.leader.Name) {
		t.Fatalf("study's wait: %v", waiting)
	}
	// Binding the agent answers the notice.
	if err := l.T.Store.InWorkspace(t.Context(), l.WS, l.Owner, func(tx *store.Tx) error {
		rt, err := tx.Q.GetRuntime(t.Context(), db.GetRuntimeParams{WorkspaceID: l.WS, ID: shared.RuntimeID})
		if err != nil {
			return err
		}
		return BindLaboratoryRuntime(t.Context(), tx, l.leader, rt)
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND kind = 'waiting_for_machine' AND resolved_at IS NULL`, l.WS).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("open notices after binding: %d", n)
	}
}

func TestOwnAgentRunUnchanged(t *testing.T) {
	l := newLabFixture(t)
	var mine db.Agent
	if err := l.T.Store.InWorkspace(t.Context(), l.WS, l.member, func(tx *store.Tx) (err error) {
		mine, err = tx.Q.CreateAgent(t.Context(), db.CreateAgentParams{WorkspaceID: l.WS, Name: "mine", Harness: "claude", Visibility: "private", OwnerID: l.member})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ownerMachine := l.register(t, l.Owner, "owner-machine", "claude", "linux")
	own := l.register(t, l.member, "member-own", "claude", "linux")
	l.run(t, mine.ID, "claude")
	if got := l.claims(t, l.Owner, ownerMachine, guest); len(got) != 0 {
		t.Fatal("someone else's machine took an unpinned study")
	}
	if got := l.claims(t, l.member, own, guest); len(got) != 1 {
		t.Fatalf("the owner's runtime claims %d", len(got))
	}
	if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE card SET machine = 'owner-machine' WHERE id = $1`, l.study); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Env.Owner.Exec(t.Context(), `INSERT INTO runtime_share (workspace_id, runtime_id, user_id) VALUES ($1, $2, $3)`, l.WS, ownerMachine.RuntimeID, l.member); err != nil {
		t.Fatal(err)
	}
	l.run(t, mine.ID, "claude")
	if got := l.claims(t, l.member, own, guest); len(got) != 0 {
		t.Fatal("a pinned study ran on its owner's other machine")
	}
	if got := l.claims(t, l.Owner, ownerMachine, guest); len(got) != 1 {
		t.Fatalf("the pinned shared machine claims %d", len(got))
	}
}

func TestOwnerMachineAutoBinds(t *testing.T) {
	l := newLabFixture(t)
	bound := func() map[string]int {
		out := map[string]int{}
		rows, err := l.Env.Owner.Query(t.Context(), `SELECT coalesce(r.daemon_id || '/' || r.harness, 'none') FROM agent a LEFT JOIN runtime r ON r.id = a.runtime_id
			WHERE a.workspace_id = $1 AND a.laboratory <> '' AND a.deleted_at IS NULL`, l.WS)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				t.Fatal(err)
			}
			out[k]++
		}
		return out
	}
	if _, err := l.T.Register(t.Context(), l.member, l.WS, "member-first", "m", []string{"claude", "codex"}, nil, agent.Platform{OS: "linux"}); err != nil {
		t.Fatal(err)
	}
	if got := bound(); len(got) != 1 || got["none"] != 18 {
		t.Fatalf("a member's machine bound: %v", got)
	}
	if _, err := l.T.Register(t.Context(), l.Owner, l.WS, "owner", "o", []string{"codex", "claude"}, nil, agent.Platform{OS: "darwin"}); err != nil {
		t.Fatal(err)
	}
	if got := bound(); len(got) != 1 || got["owner/claude"] != 18 {
		t.Fatalf("after the owner's machine: %v", got)
	}
	if _, err := l.T.Register(t.Context(), l.member, l.WS, "member-later", "m", []string{"claude"}, nil, agent.Platform{OS: "linux"}); err != nil {
		t.Fatal(err)
	}
	if got := bound(); len(got) != 1 || got["owner/claude"] != 18 {
		t.Fatalf("after a member's later machine: %v", got)
	}
}

// A rerun in a round a laboratory agent leads goes to that agent's bound
// machine, on any of its runtimes.
func TestLabRerunGoesToTheLeadersMachine(t *testing.T) {
	l := newLabFixture(t)
	own := l.register(t, l.member, "member-own", "claude", "linux")
	rs, err := l.T.Register(t.Context(), l.Owner, l.WS, "owner-box", "box", []string{"claude", "codex"}, nil, agent.Platform{OS: "linux"})
	if err != nil || len(rs) != 2 {
		t.Fatalf("register: %v", err)
	}
	var codex RuntimeRef
	for _, r := range rs {
		if r.Harness == "codex" {
			codex = RuntimeRef{WorkspaceID: l.WS, RuntimeID: r.ID, Attempt: 1}
		} else {
			l.bind(t, RuntimeRef{WorkspaceID: l.WS, RuntimeID: r.ID})
		}
	}
	if _, err := l.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'validation' WHERE id = $1`, l.study); err != nil {
		t.Fatal(err)
	}
	var rerun db.AgentRun
	if err := l.T.Store.InWorkspace(t.Context(), l.WS, l.member, func(tx *store.Tx) error {
		a := l.leader.ID
		if _, err := l.T.Enqueue(t.Context(), tx, l.member, NewAgentRun{Card: l.study, Column: board.Validation, Round: 1, Role: "leader", Harness: "claude", Prompt: "p", Agent: &a}); err != nil {
			return err
		}
		var err error
		rerun, err = l.T.EnqueueRerun(t.Context(), tx, l.member, l.study, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := l.claims(t, l.member, own, guest); len(got) != 0 {
		t.Fatalf("the study owner's machine took %d runs", len(got))
	}
	got := l.claims(t, l.Owner, codex, guest)
	if len(got) != 1 || got[0].AgentRun.ID != rerun.ID {
		t.Fatalf("the bound machine's other runtime claims %+v", got)
	}
}

// An offline bound runtime is the machine named in the offline notice, not
// the study owner's own machines.
func TestOfflineNoticeNamesTheBoundMachine(t *testing.T) {
	l := newLabFixture(t)
	own := l.register(t, l.member, "member-own", "claude", "linux")
	bound := l.register(t, l.Owner, "owner-bound", "claude", "linux")
	l.bind(t, bound)
	l.run(t, l.leader.ID, "claude")
	staleRuntime(t, l.fixture, own.RuntimeID)
	staleRuntime(t, l.fixture, bound.RuntimeID)
	if err := l.T.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := l.Env.Owner.Query(t.Context(), `SELECT user_id, runtime_id FROM inbox_item WHERE workspace_id = $1 AND kind = 'machine_offline' AND resolved_at IS NULL`, l.WS)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []uuid.UUID
	for rows.Next() {
		var user, rt uuid.UUID
		if err := rows.Scan(&user, &rt); err != nil {
			t.Fatal(err)
		}
		if user != l.Owner {
			t.Fatalf("notice went to %v, not the bound machine's owner", user)
		}
		got = append(got, rt)
	}
	if len(got) != 1 || got[0] != bound.RuntimeID {
		t.Fatalf("offline notices for %v, want only the bound runtime %v", got, bound.RuntimeID)
	}
}
