package service

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"lykeion/server/pkg/agent"
)

func TestMachineFailuresOfferTryAgain(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	for n := 1; n <= 5; n++ {
		ref, id := f.claimStart(t, runtime)
		if _, err := f.T.Fail(ctx, f.Owner, ref, id, Report{FailureReason: "provider_network", Error: "connect ECONNRESET"}); err != nil {
			t.Fatal(err)
		}
		if err := f.T.MarkHealthy(ctx, f.Owner, runtime); err != nil {
			t.Fatal(err)
		}
		f.clearBackoff(t)
	}
	var raw []byte
	var def string
	var expires time.Time
	if err := f.Env.Owner.QueryRow(ctx, "SELECT options, default_option, expires_at FROM inbox_item WHERE workspace_id = $1 AND kind = 'needs_decision' AND resolved_at IS NULL", f.WS).Scan(&raw, &def, &expires); err != nil {
		t.Fatal(err)
	}
	var opts []Option
	if err := json.Unmarshal(raw, &opts); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range opts {
		ids = append(ids, o.ID)
	}
	if !slices.Equal(ids, []string{"try_again", "park"}) || def != "park" || time.Until(expires.Add(-DecisionWindow)).Abs() > time.Minute {
		t.Fatalf("options %v default %q expires %v", ids, def, expires)
	}
}

func (f *fixture) offlineItems(t *testing.T) (n int, withOptions int) {
	t.Helper()
	if err := f.Env.Owner.QueryRow(context.Background(),
		"SELECT count(*), count(*) FILTER (WHERE options <> '[]' OR expires_at IS NOT NULL) FROM inbox_item WHERE workspace_id = $1 AND kind = 'machine_offline' AND resolved_at IS NULL", f.WS).Scan(&n, &withOptions); err != nil {
		t.Fatal(err)
	}
	return
}

func TestOfflineMachineWithQueuedWorkTellsItsOwner(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	staleRuntime(t, f, ref.RuntimeID)
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if n, opts := f.offlineItems(t); n != 1 || opts != 0 {
		t.Fatalf("items %d with options %d", n, opts)
	}
	var owner = f.Owner
	if err := f.Env.Owner.QueryRow(ctx, "SELECT user_id FROM inbox_item WHERE kind = 'machine_offline' AND workspace_id = $1", f.WS).Scan(&owner); err != nil || owner != f.Owner {
		t.Fatalf("owner %v %v", owner, err)
	}
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.offlineItems(t); n != 1 {
		t.Fatalf("a second sweep left %d items", n)
	}
	var created int
	if err := f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'inbox.created'", f.WS).Scan(&created); err != nil || created != 1 {
		t.Fatalf("inbox.created events: %d, %v", created, err)
	}
	if _, err := f.T.Register(ctx, f.Owner, f.WS, "d1", "laptop", []string{"claude"}, nil, agent.Platform{OS: "linux"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.offlineItems(t); n != 0 {
		t.Fatalf("the machine coming back left %d items", n)
	}
	// The notice was not a choice anyone made: it is dismissed, and touched
	// now so it ages into the archive from its end.
	var status string
	var fresh bool
	if err := f.Env.Owner.QueryRow(ctx, "SELECT status, touched_at > now() - interval '1 minute' AND touched_at > created_at FROM inbox_item WHERE kind = 'machine_offline' AND workspace_id = $1", f.WS).Scan(&status, &fresh); err != nil || status != "dismissed" || !fresh {
		t.Fatalf("resolved notice: %s touched now %v, %v", status, fresh, err)
	}
}
