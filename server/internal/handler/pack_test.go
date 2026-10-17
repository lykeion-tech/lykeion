package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

type packCard struct {
	ID        string
	PackID    *string `json:"pack_id"`
	StartKind string  `json:"start_kind"`
	Cap       int32
}

type packRow struct {
	ID            string
	Name          string
	Version       string
	Sha256        string
	Source        string
	UploadedBy    *string `json:"uploaded_by"`
	ApprovalsByMe bool    `json:"approvals_by_me"`
	ApprovedAt    *string `json:"approved_at"`
	PinnedStudies int     `json:"pinned_studies"`
}

// shippedFS unpacks archives into one folder per pack name, the layout of
// the embedded shipped packs.
func shippedFS(t *testing.T, zips ...[]byte) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	for _, z := range zips {
		zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			files[f.Name] = b
		}
		var m struct{ Name string }
		if err := json.Unmarshal(files["pack.json"], &m); err != nil {
			t.Fatal(err)
		}
		for name, b := range files {
			out[m.Name+"/"+name] = &fstest.MapFile{Data: b}
		}
	}
	return out
}

func (s *server) upload(t *testing.T, tok, wsID string, z []byte, out any) int {
	t.Helper()
	req, _ := http.NewRequest("POST", s.URL+"/api/packs", bytes.NewReader(z))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Workspace-ID", wsID)
	req.Header.Set("Content-Type", "application/zip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func (s *server) versions(t *testing.T, tok, wsID, name string) []packRow {
	t.Helper()
	var rows []packRow
	if c := s.call(t, "GET", "/api/packs?name="+name, tok, wsID, nil, &rows); c != 200 {
		t.Fatalf("versions: %d", c)
	}
	return rows
}

func (s *server) newCard(t *testing.T, tok, wsID string, in map[string]any) (int, packCard, string) {
	t.Helper()
	var raw json.RawMessage
	code := s.call(t, "POST", "/api/cards", tok, wsID, in, &raw)
	var c packCard
	var e struct{ Error string }
	json.Unmarshal(raw, &c)
	json.Unmarshal(raw, &e)
	return code, c, e.Error
}

func TestTeamShippedApproves(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "1", Cap: 5, Description: "Demo field"}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var catalog []struct {
		Name, Description, Version, Source string
		StartKinds                         []string `json:"start_kinds"`
	}
	if c := s.call(t, "GET", "/api/packs/catalog", a, w.ID, nil, &catalog); c != 200 || len(catalog) != 1 ||
		catalog[0].Name != "demo" || catalog[0].Version != "1" || catalog[0].Description != "Demo field" || catalog[0].StartKinds[0] != "question" {
		t.Fatalf("catalog %d %+v", c, catalog)
	}
	if c := s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "demo"}, nil); c != 200 {
		t.Fatalf("use demo: %d", c)
	}
	var me struct {
		PackName string `json:"pack_name"`
	}
	s.call(t, "GET", "/api/me", a, "", nil, &me)
	if me.PackName != "demo" {
		t.Fatalf("me %+v", me)
	}
	rows := s.versions(t, a, w.ID, "demo")
	if len(rows) != 1 || !rows[0].ApprovalsByMe || rows[0].ApprovedAt == nil || rows[0].Source != "shipped" || rows[0].UploadedBy != nil {
		t.Fatalf("versions %+v", rows)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'pack.approved' AND payload->>'pack_id' = $1", rows[0].ID); n != 1 {
		t.Fatalf("pack.approved events: %d", n)
	}
	code, c, msg := s.newCard(t, a, w.ID, map[string]any{"question": "Does X raise Y?", "start_kind": "question"})
	if code != 201 || c.PackID == nil || *c.PackID != rows[0].ID || c.Cap != 5 || c.StartKind != "question" {
		t.Fatalf("card %d %+v %s", code, c, msg)
	}
	if rows := s.versions(t, a, w.ID, "demo"); rows[0].PinnedStudies != 1 {
		t.Fatalf("pinned %+v", rows)
	}
	if c := s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "nope"}, nil); c != 400 {
		t.Fatalf("unknown Team: %d", c)
	}
}

func TestCardNeedsTeam(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	code, c, msg := s.newCard(t, a, w.ID, map[string]any{"question": "q"})
	if code != 201 || c.PackID != nil || c.Cap != 3 || c.StartKind != "" {
		t.Fatalf("packless card %d %+v %s", code, c, msg)
	}
	if code, _, _ := s.newCard(t, a, w.ID, map[string]any{"question": "q2", "start_kind": "question"}); code != 400 {
		t.Fatalf("a start kind without a Team: %d", code)
	}
	if _, err := s.Env.Owner.Exec(context.Background(), `UPDATE "user" SET pack_name = 'ghost' WHERE id = $1`, s.userID(t, a)); err != nil {
		t.Fatal(err)
	}
	if code, _, msg := s.newCard(t, a, w.ID, map[string]any{"question": "q3", "start_kind": "question"}); code != 409 || msg != "approve a version of your Team first" {
		t.Fatalf("no approved version: %d %q", code, msg)
	}
}

func TestOwnPackUpload(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "ana@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "ben@lab.org", "member")
	z := testutil.Pack(t, testutil.PackOpts{Name: "ana-pack", Version: "1"})
	var up struct{ ID, Name, Version, Sha256 string }
	if c := s.upload(t, a, w.ID, z, &up); c != 201 || up.Name != "ana-pack" || up.Version != "1" || len(up.Sha256) != 64 {
		t.Fatalf("upload %d %+v", c, up)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'pack.uploaded'"); n != 1 {
		t.Fatalf("pack.uploaded events: %d", n)
	}
	if c := s.upload(t, a, w.ID, z, nil); c != 409 {
		t.Fatalf("duplicate version: %d", c)
	}
	if c := s.upload(t, b, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "ana-pack", Version: "2"}), nil); c != 409 {
		t.Fatalf("Ben took Ana's pack name: %d", c)
	}
	var catalog []struct{ Name string }
	s.call(t, "GET", "/api/packs/catalog", b, w.ID, nil, &catalog)
	if len(catalog) != 0 {
		t.Fatalf("Ben sees %+v", catalog)
	}
	s.call(t, "GET", "/api/packs/catalog", a, w.ID, nil, &catalog)
	if len(catalog) != 1 || catalog[0].Name != "ana-pack" {
		t.Fatalf("Ana sees %+v", catalog)
	}
	if rows := s.versions(t, b, w.ID, "ana-pack"); len(rows) != 0 {
		t.Fatalf("Ben lists %+v", rows)
	}
	if c := s.call(t, "POST", "/api/packs/"+up.ID+"/approve", b, w.ID, nil, nil); c != 404 {
		t.Fatalf("Ben approved Ana's upload: %d", c)
	}
	if c := s.call(t, "PUT", "/api/me/pack", b, w.ID, map[string]string{"name": "ana-pack"}, nil); c != 400 {
		t.Fatalf("Ben chose Ana's pack: %d", c)
	}
	var e struct{ Error string }
	if c := s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "ana-pack"}, &e); c != 409 || e.Error != "approve a version of your Team first" {
		t.Fatalf("unapproved own pack: %d %q", c, e.Error)
	}
	if c := s.call(t, "POST", "/api/packs/"+up.ID+"/approve", a, w.ID, nil, nil); c != 200 {
		t.Fatalf("approve: %d", c)
	}
	if c := s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "ana-pack"}, nil); c != 200 {
		t.Fatalf("use own pack: %d", c)
	}
	code, c, msg := s.newCard(t, a, w.ID, map[string]any{"question": "q", "start_kind": "question"})
	if code != 201 || c.PackID == nil || *c.PackID != up.ID {
		t.Fatalf("card %d %+v %s", code, c, msg)
	}
}

func TestUploadShippedNameRefused(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo"}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	if c := s.upload(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "9"}), nil); c != 409 {
		t.Fatalf("upload under a shipped name: %d", c)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 {
		t.Fatalf("stored %d packs", n)
	}
}

func TestPackUploadHostile(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("../evil.md")
	f.Write([]byte("x"))
	zw.Close()
	if c := s.upload(t, a, w.ID, buf.Bytes(), nil); c != 400 {
		t.Fatalf("hostile archive: %d", c)
	}
	if c := s.upload(t, a, w.ID, []byte("not a zip"), nil); c != 400 {
		t.Fatalf("not a zip: %d", c)
	}
	err := s.H.Store.InWorkspace(context.Background(), uuid.MustParse(w.ID), uuid.MustParse(s.userID(t, a)), func(tx *store.Tx) error {
		rows, err := tx.Q.ListFieldPacks(context.Background(), tx.WS)
		if err == nil && len(rows) != 0 {
			t.Errorf("stored %+v", rows)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	stored := 0
	filepath.WalkDir(s.H.Blobs.(storage.Local).Dir, func(_ string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			stored++
		}
		return nil
	})
	if stored != 0 {
		t.Fatalf("%d blobs stored", stored)
	}
}

func TestStartKindRefused(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", StartKinds: []string{"question"}}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "demo"}, nil)
	for _, kind := range []string{"claim", ""} {
		if code, _, msg := s.newCard(t, a, w.ID, map[string]any{"question": "q", "start_kind": kind}); code != 400 {
			t.Fatalf("start kind %q: %d %s", kind, code, msg)
		}
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 0 {
		t.Fatalf("%d cards created", n)
	}
}

func TestPinnedVersion(t *testing.T) {
	s := newServer(t)
	v1 := testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "1"})
	s.H.Shipped = shippedFS(t, v1)
	a := s.signIn(t, "ana@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "ben@lab.org", "member")
	s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "demo"}, nil)
	_, old, _ := s.newCard(t, a, w.ID, map[string]any{"question": "q1", "start_kind": "question"})
	v1Row := s.versions(t, a, w.ID, "demo")[0]

	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "2"}))
	wsID, ana := uuid.MustParse(w.ID), uuid.MustParse(s.userID(t, a))
	var v2ID string
	err := s.H.Store.InWorkspace(context.Background(), wsID, ana, func(tx *store.Tx) error {
		row, err := pack.EnsureShipped(context.Background(), tx, s.H.Blobs, s.H.Shipped, "demo")
		v2ID = row.ID.String()
		return err
	})
	if err != nil || v2ID == v1Row.ID {
		t.Fatalf("ensure v2: %v %s", err, v2ID)
	}
	if c := s.call(t, "POST", "/api/packs/"+v2ID+"/approve", a, w.ID, nil, nil); c != 200 {
		t.Fatalf("approve v2: %d", c)
	}
	err = s.H.Store.InWorkspace(context.Background(), wsID, ana, func(tx *store.Tx) error {
		p, err := pack.LoadPack(context.Background(), tx, s.H.Blobs, wsID, uuid.MustParse(*old.PackID))
		if err == nil && (p.SHA256 != v1Row.Sha256 || p.Manifest.Version != "1") {
			t.Errorf("the old card loads %s v%s, want v1 %s", p.SHA256, p.Manifest.Version, v1Row.Sha256)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, fresh, _ := s.newCard(t, a, w.ID, map[string]any{"question": "q2", "start_kind": "question"}); fresh.PackID == nil || *fresh.PackID != v2ID {
		t.Fatalf("new card %+v, want v2 %s", fresh, v2ID)
	}

	if c := s.call(t, "POST", "/api/cards/"+old.ID+"/pack", b, w.ID, map[string]string{"pack_id": v2ID}, nil); c != 403 {
		t.Fatalf("Ben moved Ana's card: %d", c)
	}
	if c := s.call(t, "POST", "/api/cards/"+old.ID+"/pack", a, w.ID, map[string]string{"pack_id": v1Row.ID}, nil); c != 409 {
		t.Fatalf("move to the same version: %d", c)
	}
	var moved packCard
	if c := s.call(t, "POST", "/api/cards/"+old.ID+"/pack", a, w.ID, map[string]string{"pack_id": v2ID}, &moved); c != 200 || moved.PackID == nil || *moved.PackID != v2ID {
		t.Fatalf("move: %d %+v", c, moved)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'card.pack_moved' AND card_id = $1 AND payload->>'from' = $2 AND payload->>'to' = $3",
		old.ID, v1Row.ID, v2ID); n != 1 {
		t.Fatalf("card.pack_moved events: %d", n)
	}
}

func TestPackOtherWorkspace(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo"}))
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	s.call(t, "PUT", "/api/me/pack", a, wa.ID, map[string]string{"name": "demo"}, nil)
	id := s.versions(t, a, wa.ID, "demo")[0].ID
	if c := s.call(t, "POST", "/api/packs/"+id+"/approve", b, wb.ID, nil, nil); c != 404 {
		t.Fatalf("approved another workspace's pack: %d", c)
	}
	var mine struct{ ID string }
	s.upload(t, a, wa.ID, testutil.Pack(t, testutil.PackOpts{Name: "own"}), &mine)
	if c := s.call(t, "POST", "/api/packs/"+mine.ID+"/approve", b, wb.ID, nil, nil); c != 404 {
		t.Fatalf("approved another workspace's upload: %d", c)
	}
}
