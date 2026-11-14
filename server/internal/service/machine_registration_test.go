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

func TestMachineRegistrationCreatesNoAgents(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	blobs := storage.Local{Dir: t.TempDir()}
	agents := func() int {
		var out []db.ListAgentsRow
		if err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
			var err error
			out, err = tx.Q.ListAgents(ctx, db.ListAgentsParams{WorkspaceID: f.WS, UserID: f.Owner, IsAdmin: true})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return len(out)
	}
	if err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		return pack.InstallShipped(ctx, tx, blobs, pack.Shipped())
	}); err != nil {
		t.Fatal(err)
	}
	before := agents()
	for range 2 {
		if _, err := f.T.Register(ctx, f.Owner, f.WS, "d1", "laptop", []string{"claude", "codex"}, nil, agent.Platform{OS: "linux"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := agents(); n != before {
		t.Fatalf("registering a machine made %d agents", n-before)
	}
}
