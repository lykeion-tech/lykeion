package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

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
			card, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: "grounding", WorkspaceID: scope.ws, Number: 2, Title: "legacy", OwnerID: scope.owner, FollowsFrom: []uuid.UUID{}})
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
