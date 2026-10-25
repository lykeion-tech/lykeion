package service

import (
	"context"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func TestClaimCarriesTheStudyPythonVersion(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: f.WS, CardID: f.Card, BoardColumn: "hypotheses", Round: 1, Role: "system", Kind: "python_version", Body: []byte(`{"version":"3.11"}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	claimed, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim %+v: %v", claimed, err)
	}
	if claimed[0].PythonVersion != "3.11" {
		t.Fatalf("claim version %q", claimed[0].PythonVersion)
	}
	if err := MayWrite("leader", "python_version"); err == nil {
		t.Fatal("agent can write version record")
	}
}
