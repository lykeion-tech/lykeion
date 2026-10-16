package round

import (
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/skills"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestServeRunsBeforeEachPhaseAndCanCallTheGate(t *testing.T) {
	f := setup(t)
	var calls [][]string
	prev := StageRoles[board.Grounding]
	roles := prev
	roles.Serve = func(_ context.Context, _ db.Card, v *records.View, next []string, _ *jev.Client) (records.Batch, bool, error) {
		if v == nil {
			t.Error("Serve got no records")
		}
		calls = append(calls, slices.Clone(next))
		return records.Batch{}, len(calls) == 2, nil
	}
	StageRoles[board.Grounding] = roles
	t.Cleanup(func() { StageRoles[board.Grounding] = prev })

	ctx := context.Background()
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(ctx, f.ws, card)
	f.complete(t, f.agentRuns(t, card)[0].ID)
	f.e.Advance(ctx, f.ws, card)
	if len(calls) < 2 || !slices.Equal(calls[0], []string{ground.Scoper}) || len(calls[1]) != 4 {
		t.Fatalf("serve calls %v", calls)
	}
	var scouts, gates int
	f.env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND round = 1 AND role <> 'scoper'", card).Scan(&scouts)
	f.env.Owner.QueryRow(ctx, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'gate' AND round = 1", card).Scan(&gates)
	if scouts != 0 || gates != 1 {
		t.Fatalf("scouts %d, gates %d: when Serve asks, the gate judges at once and the phase is not queued", scouts, gates)
	}
}

// slowServe gives Grounding a server step that waits until release is closed,
// then asks for one `served` record keyed by the first role it precedes.
// entered receives once per run; calls counts the runs.
func slowServe(t *testing.T) (entered chan struct{}, release chan struct{}, calls *atomic.Int32) {
	t.Helper()
	entered, release, calls = make(chan struct{}, 8), make(chan struct{}), new(atomic.Int32)
	prev := StageRoles[board.Grounding]
	roles := prev
	roles.Serve = func(_ context.Context, _ db.Card, _ *records.View, next []string, _ *jev.Client) (records.Batch, bool, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return records.Batch{Retire: []string{"served"}, Put: []records.Put{{Kind: "served", Key: next[0], Body: map[string]string{}}}}, false, nil
	}
	StageRoles[board.Grounding] = roles
	t.Cleanup(func() { StageRoles[board.Grounding] = prev })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	return entered, release, calls
}

func TestServeHoldsNoTransaction(t *testing.T) {
	f := setup(t)
	entered, release, _ := slowServe(t)
	card := f.card(t, board.Grounding, 1)
	done := make(chan error, 1)
	go func() { done <- f.e.Advance(context.Background(), f.ws, card) }()
	<-entered
	// what F2's settle does to the card row when one of its agent runs finishes
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		return tx.Q.AddCardSpend(ctx, db.AddCardSpendParams{Usd: 0.5, ID: card, WorkspaceID: f.ws})
	})
	close(release)
	if err != nil {
		t.Fatalf("a settlement waited on the server step: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ts := f.agentRuns(t, card)
	if len(ts) != 1 || ts[0].Role != ground.Scoper || f.count(t, card, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'served'") != 1 {
		t.Fatalf("the step's record and the phase must follow it: %+v", ts)
	}
}

func TestServeOnChangedRecordsIsDropped(t *testing.T) {
	f := setup(t)
	entered, release, calls := slowServe(t)
	card := f.card(t, board.Grounding, 1)
	done := make(chan error, 1)
	go func() { done <- f.e.Advance(context.Background(), f.ws, card) }()
	<-entered
	f.env.Owner.Exec(context.Background(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, body)
		VALUES ($1, $2, 'grounding', 1, 'note', 'server', '{}')`, f.ws, card)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, card, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'served'"); calls.Load() != 2 || n != 1 || len(f.agentRuns(t, card)) != 1 {
		t.Fatalf("calls %d, served %d: the stale step must be dropped and run again", calls.Load(), n)
	}
}

func TestSharedKindsJoinTheirRoles(t *testing.T) {
	prev, had := StageRoles[board.Hypotheses]
	StageRoles[board.Hypotheses] = Roles{Phases: [][]string{{"x-role"}}, Reviewer: "x-reviewer", RoleKinds: ground.RoleKinds,
		Kinds: func() map[string]service.Kind {
			k := ground.Kinds()["verdict"]
			k.Roles = []string{"x-reviewer"}
			return map[string]service.Kind{"verdict": k}
		}}
	t.Cleanup(func() {
		if had {
			StageRoles[board.Hypotheses] = prev
		} else {
			delete(StageRoles, board.Hypotheses)
		}
	})
	k := Kinds()["verdict"]
	if !slices.Contains(k.Roles, ground.Reviewer) || !slices.Contains(k.Roles, "x-reviewer") {
		t.Fatalf("roles %v", k.Roles)
	}
	if k.Validate(json.RawMessage(`{"result":"fail"}`)) == nil {
		t.Fatal("the shared kind lost its rules")
	}
	if slices.Contains(Kinds()["evidence"].Roles, "x-reviewer") {
		t.Fatal("a kind the stage roles did not register gained their roles")
	}
	if slices.Contains(ground.Kinds()["verdict"].Roles, "x-reviewer") {
		t.Fatal("the union leaked into Grounding's own kinds")
	}
}

func TestParallelCopiesShareTheirRoleSkill(t *testing.T) {
	own := skills.Role(board.Grounding, ground.Scoper)
	if own == "" || skills.Role(board.Grounding, "scoper-2") != own {
		t.Fatal("scoper-2 must read the scoper's skill")
	}
	if skills.Role(board.Grounding, "scoper2") != "" || skills.Role(board.Grounding, "nobody-1") != "" {
		t.Fatal("only <role>-<n> falls back, and only to a skill that exists")
	}
}
