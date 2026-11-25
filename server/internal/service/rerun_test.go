package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestRerunInputsComeFromCitedProducers(t *testing.T) {
	producer, other := uuid.New(), uuid.New()
	output := func(key, name, digest string, owner uuid.UUID) db.Record {
		body, err := json.Marshal(SavedOutput{Name: name, Sha256: digest})
		if err != nil {
			t.Fatal(err)
		}
		return db.Record{Key: key, Kind: "output", AgentRunID: &owner, Body: body}
	}
	st := Standing{Records: []db.Record{
		{Key: "x1", Kind: "run", AgentRunID: &producer},
		output("o1", "outputs/helper.py", "a", producer),
		output("o2", "outputs/predictions.json", "b", producer),
		output("o3", "outputs/metric.json", "c", producer),
		output("o4", "outputs/score.py", "d", producer),
		output("o5", "outputs/unrelated.py", "e", other),
		output("o6", "outputs/ambiguous.py", "f", producer),
		output("o7", "outputs/ambiguous.py", "g", producer),
		output("o8", "outside.txt", "h", producer),
	}}
	got := st.ReplayInputs([]RerunStep{{Key: "x1", Path: "outputs/score.py", OutputPaths: []string{"outputs/metric.json"}}})
	if len(got) != 2 || got[0].Key != "o1" || got[1].Key != "o2" {
		t.Fatalf("replay inputs %+v", got)
	}
}

func (f *fixture) enqueueRerun(t *testing.T, owner uuid.UUID) uuid.UUID {
	t.Helper()
	var run db.AgentRun
	if err := f.T.Store.InWorkspace(context.Background(), f.WS, owner, func(tx *store.Tx) (err error) {
		run, err = f.T.EnqueueRerun(context.Background(), tx, owner, f.Card, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return run.ID
}

func (f *fixture) status(t *testing.T, run uuid.UUID) string {
	t.Helper()
	var s string
	if err := f.Env.Owner.QueryRow(context.Background(), "SELECT status FROM agent_run WHERE id = $1", run).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAQueuedRerunWaitsOnlyForAMachine(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ref := f.runtime(t, f.Owner, "m1", "codex")
	run := f.enqueueRerun(t, f.Owner)
	reason := func() *string {
		if err := f.T.Store.Global().MarkQueueOffline(ctx); err != nil {
			t.Fatal(err)
		}
		var r *string
		if err := f.Env.Owner.QueryRow(ctx, "SELECT queue_reason FROM agent_run WHERE id = $1", run).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := reason(); r != nil {
		t.Fatalf("a rerun with the owner's machine online waits for %q", *r)
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE runtime SET status = 'offline' WHERE id = $1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if r := reason(); r == nil || *r != "runtime_offline" {
		t.Fatalf("a rerun with no machine online: %v", r)
	}
}

// colleagueRerun is a rerun queued for a colleague's study on the owner's
// machine, shared with the whole workspace.
func colleagueRerun(t *testing.T, f *fixture) (RuntimeRef, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	ref := f.runtime(t, f.Owner, "shared-machine", "claude")
	colleague := f.member(t, "colleague@lab.org")
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE card SET owner_id = $1, machine = 'shared-machine' WHERE id = $2", colleague, f.Card); err != nil {
		t.Fatal(err)
	}
	if _, err := f.T.SetSharing(ctx, f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "workspace"}); err != nil {
		t.Fatal(err)
	}
	return ref, colleague, f.enqueueRerun(t, colleague)
}

func TestUnsharingAMachineCancelsAQueuedRerun(t *testing.T) {
	f := setup(t)
	ref, _, run := colleagueRerun(t, f)
	affected, err := f.T.SetSharing(context.Background(), f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "private"})
	if len(affected) != 1 || affected[0].RunID != run {
		t.Fatalf("affected %+v %v", affected, err)
	}
	if _, err := f.T.SetSharing(context.Background(), f.Owner, f.WS, ref.RuntimeID, SharingChange{Visibility: "private", Confirm: []uuid.UUID{run}}); err != nil {
		t.Fatal(err)
	}
	if s := f.status(t, run); s != "cancelled" {
		t.Fatalf("the rerun is %s", s)
	}
}

func TestADepartingMemberLosesQueuedReruns(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	_, colleague, run := colleagueRerun(t, f)
	if err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, f.WS); err != nil {
			return err
		}
		return f.T.RemoveMemberSharing(ctx, tx, f.Owner, colleague)
	}); err != nil {
		t.Fatal(err)
	}
	if s := f.status(t, run); s != "cancelled" {
		t.Fatalf("the rerun is %s", s)
	}
}
