package service

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func TestRemovedHarnessBacklogDoesNotBlockOrDispatch(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	const total = 97
	var legacy []uuid.UUID
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		for i := 0; i < total; i++ {
			r, err := tx.Q.EnqueueAutoresearchAgentRun(ctx, db.EnqueueAutoresearchAgentRunParams{WorkspaceID: f.WS, RequesterID: f.Owner, Harness: "cursor"})
			if err != nil {
				return err
			}
			legacy = append(legacy, r.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	old := f.runtime(t, f.Owner, "old", "cursor")
	current := f.runtime(t, f.Owner, "current", "claude")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{old}, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("removed runtime claimed: %+v, %v", got, err)
	}
	var failed int
	if err := f.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run WHERE workspace_id=$1 AND harness='cursor' AND status='failed'`, f.WS).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 32 {
		t.Fatalf("first claim settled %d removed runs, want 32", failed)
	}
	got, err = f.T.Claim(ctx, f.Owner, []RuntimeRef{current}, 1)
	if err != nil || len(got) != 1 || got[0].AgentRun.Harness != "claude" {
		t.Fatalf("supported claim: %+v, %v", got, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{old}, 1)
			if err != nil || len(got) != 0 {
				t.Errorf("concurrent removed claim: %+v, %v", got, err)
			}
		}()
	}
	wg.Wait()
	for _, id := range legacy {
		assertRemovedRun(t, f, id)
	}
	if f.settledCount() != total {
		t.Fatalf("settlement deliveries = %d, want %d", f.settledCount(), total)
	}
}

func TestRemovedHarnessCoresearchCannotBeClaimed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var run db.AgentRun
	var ordinary db.AgentRun
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		p, err := tx.Q.CreateCoresearchProject(ctx, "legacy")
		if err != nil {
			return err
		}
		chat, err := tx.Q.CreateCoresearchChat(ctx, db.CreateCoresearchChatParams{WorkspaceID: f.WS, ProjectID: p, OwnerID: f.Owner, Cli: "claude"})
		if err != nil {
			return err
		}
		run, err = tx.Q.EnqueueCoresearchAgentRun(ctx, db.EnqueueCoresearchAgentRunParams{WorkspaceID: f.WS, OwnerID: f.Owner, Harness: "cursor", Prompt: "research", CoresearchChatID: &chat.ID, ContinuityScope: "coresearch:" + chat.ID.String()})
		if err != nil {
			return err
		}
		ordinary, err = tx.Q.EnqueueAutoresearchAgentRun(ctx, db.EnqueueAutoresearchAgentRunParams{WorkspaceID: f.WS, RequesterID: f.Owner, Harness: "antigravity"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := f.runtime(t, f.Owner, "old-core", "cursor")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1, ClaimOptions{CoresearchOnly: true})
	if err != nil || len(got) != 0 {
		t.Fatalf("removed Coresearch claim: %+v, %v", got, err)
	}
	assertRemovedRun(t, f, run.ID)
	assertRemovedRun(t, f, ordinary.ID)
}

func TestRemovedHarnessMaintenanceSharesBatchBudget(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	other := uuid.New()
	for _, ws := range []uuid.UUID{f.WS, other} {
		err := f.T.Store.InWorkspace(ctx, ws, f.Owner, func(tx *store.Tx) error {
			if ws == other {
				if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: ws, Name: "other", Slug: "other"}); err != nil {
					return err
				}
				if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws, UserID: f.Owner, Role: "owner"}); err != nil {
					return err
				}
			}
			for i := 0; i < 40; i++ {
				if _, err := tx.Q.EnqueueAutoresearchAgentRun(ctx, db.EnqueueAutoresearchAgentRunParams{WorkspaceID: ws, RequesterID: f.Owner, Harness: "cursor"}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.enqueue(t, 1, "claude")
	first := f.runtime(t, f.Owner, "first", "claude")
	rs, err := f.T.Register(ctx, f.Owner, other, "second", "laptop", []string{"claude"}, nil, agent.Platform{OS: "linux"})
	if err != nil || len(rs) != 1 {
		t.Fatalf("register other runtime: %+v, %v", rs, err)
	}
	refs := []RuntimeRef{first, {WorkspaceID: other, RuntimeID: rs[0].ID, Attempt: 1}}
	got, err := f.T.Claim(ctx, f.Owner, refs, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim with maintenance: %+v, %v", got, err)
	}
	for _, ws := range []uuid.UUID{f.WS, other} {
		var failed int
		if err := f.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run WHERE workspace_id=$1 AND harness='cursor' AND status='failed'`, ws).Scan(&failed); err != nil {
			t.Fatal(err)
		}
		if failed != 32 {
			t.Fatalf("workspace %s settled %d, want 32", ws, failed)
		}
	}
	if _, err := f.T.Claim(ctx, f.Owner, refs, 1); err != nil {
		t.Fatal(err)
	}
	if f.settledCount() != 80 {
		t.Fatalf("settlement deliveries = %d, want 80", f.settledCount())
	}
}

func TestUpgradeClaimSettlesQueuedRemovedHarnesses(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var legacy []db.AgentRun
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		for _, harness := range []string{"cursor", "antigravity"} {
			a, err := tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: f.WS, OwnerID: f.Owner, Name: harness, Harness: harness, Visibility: "workspace"})
			if err != nil {
				return err
			}
			// Seed persisted pre-upgrade work, bypassing the upgraded enqueue service only.
			r, err := tx.Q.EnqueueAgentRun(ctx, db.EnqueueAgentRunParams{WorkspaceID: f.WS, CardID: &f.Card, ResponsibleUserID: f.Owner, BoardColumn: "grounding", Round: 1, Role: "agent", Harness: harness, Prompt: "inert", MaxAttempts: 2, AgentID: &a.ID})
			if err != nil {
				return err
			}
			legacy = append(legacy, r)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "upgraded", "claude")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AgentRun.Harness != "claude" {
		t.Fatalf("normal supported claim: %+v", got)
	}
	for _, r := range legacy {
		assertRemovedRun(t, f, r.ID)
	}
	if f.settledCount() != 2 {
		t.Fatalf("settlement deliveries = %d", f.settledCount())
	}
	if _, err = f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1); err != nil {
		t.Fatal(err)
	}
	if err = f.T.RecoverSettlements(ctx); err != nil {
		t.Fatal(err)
	}
	if f.settledCount() != 2 {
		t.Fatal("terminal settlement delivered again")
	}
}

func assertRemovedRun(t *testing.T, f *fixture, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		r, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: f.WS, ID: id})
		if err != nil {
			return err
		}
		if r.Status != "failed" || r.Error != agent.ErrHarnessRemoved.Error() || reasonOf(r) != "agent_error" {
			t.Fatalf("removed run remained nonterminal or unexplained: status=%s error=%q reason=%s", r.Status, r.Error, reasonOf(r))
		}
		if r.Attempt != 0 || r.RuntimeID != nil || r.StartedAt != nil || r.CompletedAt == nil {
			t.Fatalf("removed run dispatched: %+v", r)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var events, settlements, receipts, tokens int
	err = f.Env.Owner.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM event WHERE type='agent_run.failed' AND payload->>'agent_run_id'=$1),
 (SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1::uuid),
 (SELECT count(*) FROM agent_run_receipt WHERE agent_run_id=$1::uuid),
 (SELECT count(*) FROM agent_run_token WHERE agent_run_id=$1::uuid)`, id.String()).Scan(&events, &settlements, &receipts, &tokens)
	if err != nil {
		t.Fatal(err)
	}
	if events != 1 || settlements != 1 || receipts != 0 || tokens != 0 {
		t.Fatalf("events=%d settlements=%d receipts=%d tokens=%d", events, settlements, receipts, tokens)
	}
}

func TestRemovedHarnessQueueRespectsOwnerAndWorkspace(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	other, err := f.T.Store.Global().UpsertUserByEmail(ctx, "other@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	otherWS := uuid.New()
	var untouched []uuid.UUID
	for _, scope := range []struct{ ws, owner uuid.UUID }{{f.WS, other.ID}, {otherWS, f.Owner}} {
		err := f.T.Store.InWorkspace(ctx, scope.ws, scope.owner, func(tx *store.Tx) error {
			if scope.ws == otherWS {
				if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: scope.ws, Name: "other", Slug: "other"}); err != nil {
					return err
				}
			}
			if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: scope.ws, UserID: scope.owner, Role: "owner"}); err != nil {
				return err
			}
			card, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: "grounding", WorkspaceID: scope.ws, Number: 2, Title: "legacy", OwnerID: scope.owner})
			if err != nil {
				return err
			}
			run, err := tx.Q.EnqueueAgentRun(ctx, db.EnqueueAgentRunParams{WorkspaceID: scope.ws, CardID: &card.ID, ResponsibleUserID: scope.owner, BoardColumn: "grounding", Round: 1, Role: "agent", Harness: "cursor", MaxAttempts: 2})
			untouched = append(untouched, run.ID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// A legacy card-less run uses its requester, as normal claims do.
	var mine db.AgentRun
	err = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) (err error) {
		mine, err = tx.Q.EnqueueAutoresearchAgentRun(ctx, db.EnqueueAutoresearchAgentRunParams{WorkspaceID: f.WS, RequesterID: f.Owner, Harness: "antigravity"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := f.runtime(t, f.Owner, "supported", "codex")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("claimed removed run: %+v", got)
	}
	assertRemovedRun(t, f, mine.ID)
	for _, id := range untouched {
		var status string
		if err := f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "queued" {
			t.Fatalf("another owner/workspace was mutated: %s", status)
		}
	}
}
