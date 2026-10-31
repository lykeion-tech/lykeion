package service

import (
	"context"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func TestRegisteringAMachineAddsTheBuiltInTeamsAgents(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	blobs := storage.Local{Dir: t.TempDir()}
	agents := func() []db.ListAgentsRow {
		var out []db.ListAgentsRow
		if err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
			var err error
			out, err = tx.Q.ListAgents(ctx, db.ListAgentsParams{WorkspaceID: f.WS, UserID: f.Owner})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		return pack.InstallShipped(ctx, tx, blobs, pack.Shipped())
	}); err != nil {
		t.Fatal(err)
	}
	if n := len(agents()); n != 0 {
		t.Fatalf("agents before a machine: %d", n)
	}
	for range 2 {
		if _, err := f.T.Register(ctx, f.Owner, f.WS, "d1", "laptop", []string{"claude"}, nil, agent.Platform{OS: "linux"}); err != nil {
			t.Fatal(err)
		}
	}
	want := 0
	for _, name := range pack.ShippedNames(pack.Shipped()) {
		_, p, err := pack.ReadShipped(pack.Shipped(), name)
		if err != nil {
			t.Fatal(err)
		}
		want += 2 + len(p.Manifest.Members)
	}
	got := agents()
	if len(got) != want {
		t.Fatalf("agents after registering twice: %d, want %d", len(got), want)
	}
	for _, a := range got {
		if !a.TeamMember || a.Harness != "claude" || a.OwnerID != f.Owner {
			t.Fatalf("agent %+v", a)
		}
	}
}
