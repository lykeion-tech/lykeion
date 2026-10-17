package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
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
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "1", Cap: 5, Description: "Demo field",
		Columns: []string{"grounding", "experimentation"}}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var catalog []struct {
		Name, Description, Version, Source string
		StartKinds                         []string `json:"start_kinds"`
		Agents                             []struct {
			Column string
			Roles  []string
		}
	}
	if c := s.call(t, "GET", "/api/packs/catalog", a, w.ID, nil, &catalog); c != 200 || len(catalog) != 1 ||
		catalog[0].Name != "demo" || catalog[0].Version != "1" || catalog[0].Description != "Demo field" || catalog[0].StartKinds[0] != "question" ||
		len(catalog[0].Agents) != 2 || catalog[0].Agents[0].Column != "grounding" || catalog[0].Agents[1].Column != "experimentation" ||
		!slices.Equal(catalog[0].Agents[0].Roles, []string{"author"}) {
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
	if c := s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "demo"}, nil); c != 200 {
		t.Fatalf("use demo again: %d", c)
	}
	if c := s.call(t, "POST", "/api/packs/"+rows[0].ID+"/approve", a, w.ID, nil, nil); c != 200 {
		t.Fatalf("approve again: %d", c)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'pack.approved'"); n != 1 {
		t.Fatalf("a repeated approval wrote another event: %d", n)
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
	// Workspace events reach every member, so an upload's events name only its id.
	if n := s.count(t, "SELECT count(*) FROM event WHERE type IN ('pack.uploaded', 'pack.approved') AND payload->>'pack_id' = $1", up.ID); n != 2 {
		t.Fatalf("upload events: %d", n)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type IN ('pack.uploaded', 'pack.approved') AND payload - 'pack_id' <> '{}'::jsonb"); n != 0 {
		t.Fatalf("%d upload events carry more than the pack id", n)
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

func TestDatasetStudyTakesItsDataset(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", StartKinds: []string{"question", "dataset"}}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "demo"}, nil)
	for _, body := range []map[string]any{
		{"question": "q", "start_kind": "dataset"},
		{"question": "q", "start_kind": "dataset", "dataset": "   "},
		{"question": "q", "start_kind": "dataset", "dataset": "two words"},
		{"question": "q", "start_kind": "dataset", "dataset": "ftp://example.org/x"},
		{"question": "q", "start_kind": "question", "dataset": "GSE84133"},
	} {
		if code, _, msg := s.newCard(t, a, w.ID, body); code != 400 {
			t.Fatalf("%v: %d %s", body, code, msg)
		}
	}
	if n := s.count(t, "SELECT count(*) FROM card"); n != 0 {
		t.Fatalf("%d cards created by refused requests", n)
	}
	for i, ref := range []string{"GSE84133", "https://zenodo.org/records/123", "10.5281/zenodo.123"} {
		code, c, msg := s.newCard(t, a, w.ID, map[string]any{"question": "q" + strconv.Itoa(i), "start_kind": "dataset", "dataset": " " + ref + " "})
		if code != 201 {
			t.Fatalf("%s: %d %s", ref, code, msg)
		}
		var got string
		if err := s.Env.Owner.QueryRow(context.Background(),
			`SELECT body->>'ref' FROM record WHERE card_id = $1 AND kind = 'dataset_ref' AND superseded_at IS NULL`, c.ID).Scan(&got); err != nil {
			t.Fatalf("%s: no dataset_ref record: %v", ref, err)
		}
		if got != ref {
			t.Fatalf("recorded %q, want %q", got, ref)
		}
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
	// The pinned version's roles are mid-run: the move waits for them.
	s.queue(t, w.ID, old.ID, s.userID(t, a), "claude")
	var e struct{ Error string }
	if c := s.call(t, "POST", "/api/cards/"+old.ID+"/pack", a, w.ID, map[string]string{"pack_id": v2ID}, &e); c != 409 || e.Error != "wait for this study's running work to finish" {
		t.Fatalf("moved while running: %d %q", c, e.Error)
	}
	if n := s.count(t, "SELECT count(*) FROM card WHERE id = $1 AND pack_id = $2", old.ID, v1Row.ID); n != 1 {
		t.Fatalf("a refused move changed the card's pack")
	}
	if _, err := s.Env.Owner.Exec(context.Background(), `UPDATE agent_run SET status = 'cancelled' WHERE card_id = $1`, old.ID); err != nil {
		t.Fatal(err)
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

// withFiles copies an archive and adds stored, incompressible files to it.
func withFiles(t *testing.T, z []byte, files map[string][]byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if err := zw.Copy(f); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err == nil {
			_, err = w.Write(body)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (s *server) storedBlobs(t *testing.T) int {
	n := 0
	filepath.WalkDir(s.H.Blobs.(storage.Local).Dir, func(_ string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestPackUploadLarge(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	noise := func() []byte { b := make([]byte, 900<<10); rand.Read(b); return b }
	z := withFiles(t, testutil.Pack(t, testutil.PackOpts{Name: "big"}), map[string][]byte{
		"skills/grounding/author/a.bin": noise(), "skills/grounding/author/b.bin": noise()})
	if len(z) <= 1<<20 || len(z) > pack.MaxArchive {
		t.Fatalf("archive is %d bytes", len(z))
	}
	var up struct{ Name string }
	if c := s.upload(t, a, w.ID, z, &up); c != 201 || up.Name != "big" {
		t.Fatalf("large upload: %d %+v", c, up)
	}
	if s.storedBlobs(t) != 1 {
		t.Fatal("the archive was not stored")
	}
}

func TestPackUploadOversize(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	if c := s.upload(t, a, w.ID, make([]byte, pack.MaxArchive+1), nil); c != 413 {
		t.Fatalf("oversize upload: %d", c)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 || s.storedBlobs(t) != 0 {
		t.Fatalf("stored %d packs, %d blobs", n, s.storedBlobs(t))
	}
}

// TestRoleRecordKindsPerCard checks a role on a pack card writes only its
// pack's kinds, never the legacy stages' kinds nor a server kind the pack
// declares, while a legacy card keeps the legacy kinds.
func TestRoleRecordKindsPerCard(t *testing.T) {
	s := newServer(t)
	_, wsID, c, run := s.roleFixture(t)
	post := func(token, kind string, body any) int {
		return s.call(t, "POST", "/api/role/records", token, "", map[string]any{"kind": kind, "body": body}, nil)
	}
	if got := post(run.token, "question", map[string]string{"text": "Scoped?"}); got != 201 {
		t.Fatalf("legacy question: %d", got)
	}
	s.pinPack(t, wsID, c.ID)
	for _, tc := range []struct {
		kind string
		body any
		want int
	}{
		{"note", map[string]string{"text": "a note"}, 201},
		{"question", map[string]string{"text": "Scoped?"}, 400},
		{"fetch", map[string]string{"source_id": "openalex:W1"}, 400},
		{"verdict", map[string]string{"result": "pass", "reason": "ok"}, 403},
		{"nothing_found", map[string]string{"reason": "none"}, 201},
	} {
		if got := post(run.token, tc.kind, tc.body); got != tc.want {
			t.Fatalf("%s: %d, want %d", tc.kind, got, tc.want)
		}
	}
}

// pinPack stores a pack, unique to the call, whose grounding scoper writes
// note (and declares fetch), and pins the card to it.
func (s *server) pinPack(t *testing.T, wsID, cardID string) db.FieldPack {
	t.Helper()
	z := testutil.Zip(t, map[string]string{
		"pack.json": `{"name": "field", "version": "1", "start_kinds": ["question"],
			"columns": {"grounding": {"phases": [["scoper"]], "reviewer": "rev", "kinds": {"note": {"roles": ["scoper"]}, "fetch": {"roles": ["scoper"]}}}},
			"failures": {"premise_unsupported": "grounding"}}`,
		"skills/grounding/scoper/SKILL.md": "Scope. " + uuid.NewString(),
		"skills/grounding/rev/SKILL.md":    "Review.",
		"checks/grounding.star":            "def check(records):\n    return []\n",
	})
	p, err := pack.Parse(z)
	if err != nil {
		t.Fatal(err)
	}
	var row db.FieldPack
	if err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(wsID), uuid.Nil, func(tx *store.Tx) (err error) {
		row, err = pack.Insert(t.Context(), tx, s.H.Blobs, z, p, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET pack_id = $1 WHERE id = $2`, row.ID, cardID); err != nil {
		t.Fatal(err)
	}
	return row
}

// TestRoleRecordPackUnloadable checks a role write on a card whose pack blob
// is gone answers 503 and stores nothing.
func TestRoleRecordPackUnloadable(t *testing.T) {
	s := newServer(t)
	_, wsID, c, run := s.roleFixture(t)
	row := s.pinPack(t, wsID, c.ID)
	if err := os.Remove(filepath.Join(s.H.Blobs.(storage.Local).Dir, row.BlobKey)); err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if got := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "note", "body": map[string]string{"text": "a note"}}, &out); got != 503 || out["error"] != "this study's Team can't be loaded" {
		t.Fatalf("%d %v", got, out)
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM record WHERE card_id = $1`, c.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("records %d %v", n, err)
	}
}

// TestRoleRecordWithoutKinds checks a server with no kinds wired refuses
// every kind as unknown.
func TestRoleRecordWithoutKinds(t *testing.T) {
	s := newServer(t)
	s.H.KindsFor = nil
	_, _, _, run := s.roleFixture(t)
	if got := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "question", "body": map[string]string{"text": "Scoped?"}}, nil); got != 400 {
		t.Fatalf("%d", got)
	}
}

func TestCardShowsItsTeam(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "1"}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, bare, _ := s.newCard(t, a, w.ID, map[string]any{"question": "q"})
	if c := s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "demo"}, nil); c != 200 {
		t.Fatalf("use demo: %d", c)
	}
	_, pinned, _ := s.newCard(t, a, w.ID, map[string]any{"question": "Does X raise Y?", "start_kind": "question"})
	for _, tc := range []struct{ id, name, version string }{{bare.ID, "", ""}, {pinned.ID, "demo", "1"}} {
		var got struct {
			ID          string
			PackName    *string `json:"pack_name"`
			PackVersion *string `json:"pack_version"`
		}
		if c := s.call(t, "GET", "/api/cards/"+tc.id, a, w.ID, nil, &got); c != 200 || got.ID != tc.id ||
			got.PackName == nil || *got.PackName != tc.name || got.PackVersion == nil || *got.PackVersion != tc.version {
			t.Fatalf("card %s: %d %+v", tc.id, c, got)
		}
	}
}

func TestCardRecordsOfOneKind(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, c, _ := s.newCard(t, a, w.ID, map[string]any{"question": "q"})
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body, superseded_at) VALUES
		($1, $2, 'grounding', 1, 'starting_point', 'server', '0', '{}', NULL),
		($1, $2, 'experimentation', 2, 'starting_point', 'server', '1', '{}', NULL),
		($1, $2, 'experimentation', 2, 'starting_point', 'server', '2', '{}', now()),
		($1, $2, 'experimentation', 2, 'result', 'tester', 'R1', '{}', NULL)`, w.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	var rows []struct{ Kind, Key string }
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/records?kind=starting_point", a, w.ID, nil, &rows); code != 200 ||
		len(rows) != 2 || rows[0].Key+rows[1].Key != "01" && rows[0].Key+rows[1].Key != "10" || rows[0].Kind != "starting_point" {
		t.Fatalf("records %d %+v", code, rows)
	}
}

func TestColleagueReadsTheStudysUploadedTeam(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "ana@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "ben@lab.org", "member")
	var up struct{ ID string }
	if c := s.upload(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "ana-pack", Version: "4"}), &up); c != 201 {
		t.Fatalf("upload %d", c)
	}
	if c := s.call(t, "POST", "/api/packs/"+up.ID+"/approve", a, w.ID, nil, nil); c != 200 {
		t.Fatalf("approve %d", c)
	}
	if c := s.call(t, "PUT", "/api/me/pack", a, w.ID, map[string]string{"name": "ana-pack"}, nil); c != 200 {
		t.Fatalf("use ana-pack %d", c)
	}
	_, card, _ := s.newCard(t, a, w.ID, map[string]any{"question": "Does X raise Y?", "start_kind": "question"})
	var got struct {
		PackName    string `json:"pack_name"`
		PackVersion string `json:"pack_version"`
	}
	if c := s.call(t, "GET", "/api/cards/"+card.ID, b, w.ID, nil, &got); c != 200 || got.PackName != "ana-pack" || got.PackVersion != "4" {
		t.Fatalf("Ben reads %d %+v", c, got)
	}
}

func TestCardRecordById(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, c, _ := s.newCard(t, a, w.ID, map[string]any{"question": "q"})
	_, other, _ := s.newCard(t, a, w.ID, map[string]any{"question": "q2"})
	var live, gone, foreign string
	for _, row := range []struct {
		card, sup string
		into      *string
	}{{c.ID, "NULL", &live}, {c.ID, "now()", &gone}, {other.ID, "NULL", &foreign}} {
		if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body, superseded_at)
			VALUES ($1, $2, 'experimentation', 2, 'result', 'tester', 'R1', '{}', `+row.sup+`) RETURNING id`, w.ID, row.card).Scan(row.into); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		id string
		n  int
	}{{live, 1}, {gone, 0}, {foreign, 0}, {"not-a-uuid", 0}} {
		var rows []struct{ ID string }
		if code := s.call(t, "GET", "/api/cards/"+c.ID+"/records?id="+tc.id, a, w.ID, nil, &rows); code != 200 || len(rows) != tc.n || (tc.n == 1 && rows[0].ID != tc.id) {
			t.Fatalf("id %s: %d %+v", tc.id, code, rows)
		}
	}
}
