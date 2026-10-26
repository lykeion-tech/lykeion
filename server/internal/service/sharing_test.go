package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestRuntimeAllowsUserRules(t *testing.T) {
	f := setup(t)
	ref := f.runtime(t, f.Owner, "share", "claude")
	ctx := context.Background()
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		rt, e := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: f.WS, ID: ref.RuntimeID})
		if e != nil {
			return e
		}
		listed := uuid.New()
		if _, e := tx.Q.SetRuntimeSharing(ctx, db.SetRuntimeSharingParams{WorkspaceID: f.WS, ID: ref.RuntimeID, OwnerID: f.Owner, Visibility: "private"}); e != nil {
			return e
		}
		member, e := f.T.Store.Global().UpsertUserByEmail(ctx, "listed@lab.org")
		if e != nil {
			return e
		}
		listed = member.ID
		if e := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: listed, Role: "member"}); e != nil {
			return e
		}
		if e := tx.Q.ReplaceRuntimeShares(ctx, db.ReplaceRuntimeSharesParams{WorkspaceID: f.WS, RuntimeID: ref.RuntimeID, UserIds: []uuid.UUID{listed}}); e != nil {
			return e
		}
		for _, tc := range []struct {
			u    uuid.UUID
			v    string
			want bool
		}{{f.Owner, "private", true}, {uuid.New(), "workspace", true}, {uuid.New(), "private", false}, {listed, "private", true}} {
			rt.Visibility = tc.v
			got, e := RuntimeAllowsUser(ctx, tx, rt, tc.u)
			if e != nil || got != tc.want {
				t.Fatalf("allows %s: %v %v", tc.v, got, e)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestNarrowingWithColleagueRunsAsksFirst(t *testing.T)  { testSharingRevocation(t, false, "linux") }
func TestAConfirmedRevokeCancelsColleagueRuns(t *testing.T) { testSharingRevocation(t, true, "linux") }
func TestAStaleConfirmIsRefused(t *testing.T)               { testSharingRevocation(t, true, "linux") }
func testSharingRevocation(t *testing.T, confirm bool, platform string) {
	f := setup(t)
	ctx := context.Background()
	ref := f.runtime(t, f.Owner, "shared-machine", "claude")
	colleague, e := f.T.Store.Global().UpsertUserByEmail(ctx, "colleague@lab.org")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Env.Owner.Exec(ctx, "UPDATE \"user\" SET name='Colleague' WHERE id=$1", colleague.ID); e != nil {
		t.Fatal(e)
	}
	f.enqueue(t, 1, "claude")
	var ownRun uuid.UUID
	if e = f.Env.Owner.QueryRow(ctx, "SELECT id FROM agent_run WHERE card_id=$1", f.Card).Scan(&ownRun); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Env.Owner.Exec(ctx, "UPDATE agent_run SET status='running',runtime_id=$1 WHERE id=$2", ref.RuntimeID, ownRun); e != nil {
		t.Fatal(e)
	}
	var run uuid.UUID
	e = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		if e := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: colleague.ID, Role: "member"}); e != nil {
			return e
		}
		_, e := f.Env.Owner.Exec(ctx, "UPDATE card SET owner_id=$1,machine='shared-machine' WHERE id=$2", colleague.ID, f.Card)
		if e != nil {
			return e
		}
		r, e := f.T.Enqueue(ctx, tx, colleague.ID, NewAgentRun{Card: f.Card, Column: "grounding", Round: 1, Role: "colleague", Harness: "claude", Prompt: "p", MaxAttempts: 2})
		run = r.ID
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.T.SetSharing(ctx, f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "workspace"}); e != nil {
		t.Fatal(e)
	}
	if platform != "linux" {
		if _, err := f.Env.Owner.Exec(ctx, "UPDATE runtime SET os=$1 WHERE id=$2", platform, ref.RuntimeID); err != nil {
			t.Fatal(err)
		}
	}
	affected, e := f.T.SetSharing(ctx, f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "private"})
	if !errors.Is(e, ErrSharingConfirmation) || len(affected) != 1 || affected[0].RunID != run || affected[0].StudyTitle != "t" || affected[0].Colleague != "Colleague" {
		t.Fatalf("affected %+v %v", affected, e)
	}
	if t.Name() == "TestAStaleConfirmIsRefused" {
		var second uuid.UUID
		e = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
			r, e := f.T.Enqueue(ctx, tx, colleague.ID, NewAgentRun{Card: f.Card, Column: "grounding", Round: 1, Role: "auditor", Harness: "claude", Prompt: "p", MaxAttempts: 2})
			second = r.ID
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		affected, e = f.T.SetSharing(ctx, f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "private", Confirm: []uuid.UUID{run}})
		if !errors.Is(e, ErrSharingConfirmation) || len(affected) != 2 {
			t.Fatalf("stale %+v %v", affected, e)
		}
		for _, id := range []uuid.UUID{run, second} {
			var status string
			if e = f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", id).Scan(&status); e != nil || status != "queued" {
				t.Fatalf("status %s %v", status, e)
			}
		}
		return
	}
	if confirm {
		defer func() {
			var status string
			if e := f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", ownRun).Scan(&status); e != nil || status != "running" {
				t.Fatalf("owner run %s %v", status, e)
			}
		}()
		if _, e = f.T.SetSharing(ctx, f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "private", Confirm: []uuid.UUID{run}}); e != nil {
			t.Fatal(e)
		}
		var status string
		var reason *string
		if e = f.Env.Owner.QueryRow(ctx, "SELECT status,cancel_reason FROM agent_run WHERE id=$1", run).Scan(&status, &reason); e != nil {
			t.Fatal(e)
		}
		var settlements int
		if e = f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1", run).Scan(&settlements); e != nil || settlements != 1 {
			t.Fatalf("settlements %d %v", settlements, e)
		}
		if status != "cancelled" || reason == nil || *reason != "runtime_access_revoked" {
			t.Fatalf("%s %v", status, reason)
		}
	}
}

func TestWideningNeverAsks(t *testing.T) {
	f := setup(t)
	ref := f.runtime(t, f.Owner, "wide", "claude")
	if affected, err := f.T.SetSharing(context.Background(), f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "workspace", Confirm: []uuid.UUID{uuid.New()}}); err != nil || len(affected) != 0 {
		t.Fatalf("%+v %v", affected, err)
	}
}

func TestSharingDropsOwnerAndDuplicateMembers(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ref := f.runtime(t, f.Owner, "clean", "codex")
	member, e := f.T.Store.Global().UpsertUserByEmail(ctx, "share-member@lab.org")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: member.ID, Role: "member"})
	}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e = f.T.SetSharing(ctx, f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "private", SharedWith: []uuid.UUID{f.Owner, member.ID, member.ID}}); e != nil {
			t.Fatal(e)
		}
	}
	if e = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		ids, e := tx.Q.RuntimeShares(ctx, db.RuntimeSharesParams{WorkspaceID: f.WS, RuntimeID: ref.RuntimeID})
		if e != nil {
			return e
		}
		if len(ids) != 1 || ids[0] != member.ID {
			t.Fatalf("shares %+v", ids)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestOnlyLinuxRuntimesCanBeShared(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "", "windows"} {
		t.Run(platform, func(t *testing.T) {
			f := setup(t)
			ref := f.runtime(t, f.Owner, "platform", "codex")
			if _, err := f.Env.Owner.Exec(t.Context(), "UPDATE runtime SET os=$1 WHERE id=$2", platform, ref.RuntimeID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.T.SetSharing(t.Context(), uuid.New(), f.WS, ref.RuntimeID, SharingChange{Visibility: "workspace"}); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("owner authorization: %v", err)
			}
			_, err := f.T.SetSharing(t.Context(), f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "workspace"})
			if platform == "linux" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != "Shared runs require Linux. On a Mac, register a Linux VM as a separate machine." {
				t.Fatalf("unsupported sharing: %v", err)
			}
		})
	}
}
func TestOwnerAccessDoesNotRequireLinux(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "", "windows"} {
		rt := db.Runtime{OwnerID: uuid.New(), Os: platform, Visibility: "workspace"}
		got, err := RuntimeAllowsUser(t.Context(), nil, rt, rt.OwnerID)
		if err != nil || !got {
			t.Fatalf("owner %s: %t %v", platform, got, err)
		}
		if platform != "linux" {
			got, err = RuntimeAllowsUser(t.Context(), nil, rt, uuid.New())
			if err != nil || got {
				t.Fatalf("guest %s: %t %v", platform, got, err)
			}
		}
	}
}
func TestUnsupportedSharingCanBeCleared(t *testing.T) { testSharingRevocation(t, true, "darwin") }
