package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func shippedOf(t *testing.T, name string) fstest.MapFS {
	t.Helper()
	z := testutil.Pack(t, testutil.PackOpts{Name: name, Stage: "grounding"})
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[name+"/"+f.Name] = &fstest.MapFile{Data: b}
	}
	return out
}

func installLab(t *testing.T) (*store.Store, uuid.UUID, uuid.UUID, storage.Storage) {
	t.Helper()
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	ctx := context.Background()
	u, err := st.Global().UpsertUserByEmail(ctx, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	ws := uuid.New()
	err = st.InWorkspace(ctx, ws, u.ID, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: ws, Slug: "lab", Name: "Lab"}); err != nil {
			return err
		}
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws, UserID: u.ID, Role: "owner"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, ws, u.ID, storage.Local{Dir: t.TempDir()}
}

func installOnce(t *testing.T, st *store.Store, ws, user uuid.UUID, blobs storage.Storage, shipped fstest.MapFS) error {
	return st.InWorkspace(context.Background(), ws, user, func(tx *store.Tx) error {
		return InstallShipped(context.Background(), tx, blobs, shipped)
	})
}

func rowsOf(t *testing.T, st *store.Store, ws, user uuid.UUID) []db.StoreTeamsRow {
	t.Helper()
	var rows []db.StoreTeamsRow
	err := st.InWorkspace(context.Background(), ws, user, func(tx *store.Tx) error {
		var err error
		rows, err = tx.Q.StoreTeams(context.Background(), db.StoreTeamsParams{WorkspaceID: ws, UserID: user})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestInstallShippedAddsMissingTeams(t *testing.T) {
	st, ws, user, blobs := installLab(t)
	shipped := shippedOf(t, "Grounding Team")
	if err := installOnce(t, st, ws, user, blobs, shipped); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, st, ws, user)
	if len(rows) != 1 || rows[0].Name != "Grounding Team" || rows[0].Source != "shipped" || !rows[0].Added {
		t.Fatalf("rows %+v", rows)
	}
}

func TestInstallShippedTwiceKeepsOneRow(t *testing.T) {
	st, ws, user, blobs := installLab(t)
	shipped := shippedOf(t, "Grounding Team")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = installOnce(t, st, ws, user, blobs, shipped) }()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if rows := rowsOf(t, st, ws, user); len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestInstallRenamesAWorkspaceTeamWithTheSameName(t *testing.T) {
	st, ws, user, blobs := installLab(t)
	ctx := context.Background()
	p, err := Parse(testutil.Pack(t, testutil.PackOpts{Name: "Grounding Team", Stage: "grounding"}))
	if err != nil {
		t.Fatal(err)
	}
	err = st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		_, err := Insert(ctx, tx, "k", p, &user)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := installOnce(t, st, ws, user, blobs, shippedOf(t, "Grounding Team")); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var custom db.FieldPack
	err = st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		rows, err := tx.Q.ListOwnPacks(ctx, db.ListOwnPacksParams{WorkspaceID: ws, UserID: &user})
		if len(rows) == 1 {
			custom = rows[0]
		}
		for _, r := range rows {
			got[r.Source] = r.Name
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Name string }
	if err := json.Unmarshal(custom.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	if got["uploaded"] != "Grounding Team (custom)" || m.Name != "Grounding Team (custom)" {
		t.Fatalf("upload %v manifest %q", got, m.Name)
	}
	if rows := rowsOf(t, st, ws, user); len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestInstallPicksAFreeNameWhenCustomIsTaken(t *testing.T) {
	st, ws, user, blobs := installLab(t)
	ctx := context.Background()
	for _, name := range []string{"Grounding Team", "Grounding Team (custom)"} {
		p, err := Parse(testutil.Pack(t, testutil.PackOpts{Name: name, Stage: "grounding"}))
		if err != nil {
			t.Fatal(err)
		}
		err = st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
			_, err := Insert(ctx, tx, "k", p, &user)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := installOnce(t, st, ws, user, blobs, shippedOf(t, "Grounding Team")); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, r := range rowsOf(t, st, ws, user) {
		names[r.Name] = true
	}
	for _, want := range []string{"Grounding Team", "Grounding Team (custom)", "Grounding Team (custom 2)"} {
		if !names[want] {
			t.Fatalf("missing %q in %v", want, names)
		}
	}
}
