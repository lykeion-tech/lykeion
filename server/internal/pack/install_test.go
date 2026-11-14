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

// withInstructions is shipped with the leader's and first member's
// instructions changed, as a later Lykeion release would ship it.
func withInstructions(t *testing.T, shipped fstest.MapFS, name, leader, member string) fstest.MapFS {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(shipped[name+"/pack.json"].Data, &m); err != nil {
		t.Fatal(err)
	}
	m["leader"].(map[string]any)["agent"].(map[string]any)["instructions"] = leader
	m["members"].([]any)[0].(map[string]any)["agent"].(map[string]any)["instructions"] = member
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for k, v := range shipped {
		out[k] = v
	}
	out[name+"/pack.json"] = &fstest.MapFile{Data: b}
	return out
}

func TestInstallShippedRefreshesAnOlderShippedTeam(t *testing.T) {
	st, ws, user, blobs := installLab(t)
	ctx := context.Background()
	old := shippedOf(t, "Grounding Team")
	if err := installOnce(t, st, ws, user, blobs, old); err != nil {
		t.Fatal(err)
	}
	// The owner's agents for the Team: the leader's as added, the member's
	// edited by its owner.
	var row db.FieldPack
	agents := map[string]uuid.UUID{}
	err := st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		var err error
		if row, err = tx.Q.ShippedPack(ctx, db.ShippedPackParams{WorkspaceID: ws, Name: "Grounding Team"}); err != nil {
			return err
		}
		for member, instr := range map[string]string{"pi": "You are pi.", "member-1": "My own edit."} {
			a, err := tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: ws, Name: member + " · Grounding Team", Instructions: instr,
				Harness: "claude", Visibility: "private", OwnerID: user, ConversationStarters: []byte("[]")})
			if err != nil {
				return err
			}
			agents[member] = a.ID
			if err := tx.Q.InsertTeamAgent(ctx, db.InsertTeamAgentParams{WorkspaceID: ws, PackID: row.ID, UserID: &user, Member: member, AgentID: a.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	current := func() (db.FieldPack, map[string]string) {
		t.Helper()
		var r db.FieldPack
		instr := map[string]string{}
		err := st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
			var err error
			if r, err = tx.Q.ShippedPack(ctx, db.ShippedPackParams{WorkspaceID: ws, Name: "Grounding Team"}); err != nil {
				return err
			}
			for member, id := range agents {
				a, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: ws, ID: id})
				if err != nil {
					return err
				}
				instr[member] = a.Instructions
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return r, instr
	}

	// The same pack again changes nothing.
	if err := installOnce(t, st, ws, user, blobs, old); err != nil {
		t.Fatal(err)
	}
	if r, _ := current(); r.Sha256 != row.Sha256 || r.BlobKey != row.BlobKey {
		t.Fatalf("an unchanged pack rewrote the row: sha %s blob %s", r.Sha256, r.BlobKey)
	}

	next := withInstructions(t, old, "Grounding Team", "You are pi, and you declare items.", "You are member-1, and you declare items.")
	if err := installOnce(t, st, ws, user, blobs, next); err != nil {
		t.Fatal(err)
	}
	r, instr := current()
	if r.ID != row.ID || r.Sha256 == row.Sha256 || r.BlobKey == row.BlobKey {
		t.Fatalf("the shipped row was not refreshed in place: id %s→%s sha %s→%s", row.ID, r.ID, row.Sha256, r.Sha256)
	}
	var m Manifest
	if err := json.Unmarshal(r.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.Leader.Agent.Instructions != "You are pi, and you declare items." {
		t.Fatalf("the row keeps the old manifest: %q", m.Leader.Agent.Instructions)
	}
	if instr["pi"] != "You are pi, and you declare items." {
		t.Errorf("the untouched leader agent keeps %q", instr["pi"])
	}
	if instr["member-1"] != "My own edit." {
		t.Errorf("the edited member agent lost its owner's edit: %q", instr["member-1"])
	}
}
