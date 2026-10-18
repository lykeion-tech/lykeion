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
	"path/filepath"
	"strings"
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
	ID     string
	PackID *string `json:"pack_id"`
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

// shipOne files a shipped pack the way a deployment that ships Teams would.
func shipOne(ctx context.Context, tx *store.Tx, blobs storage.Storage, shipped fs.FS, name string) (db.FieldPack, error) {
	z, p, err := pack.ReadShipped(shipped, name)
	if err != nil {
		return db.FieldPack{}, err
	}
	key, err := pack.PutBlob(ctx, blobs, tx.WS, z, p.SHA256)
	if err != nil {
		return db.FieldPack{}, err
	}
	return pack.Insert(ctx, tx, key, p, nil)
}

func TestApprovingAShippedPackIsRecordedOnce(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "1", Description: "Demo field"}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	wsID, ana := uuid.MustParse(w.ID), uuid.MustParse(s.userID(t, a))
	var row db.FieldPack
	err := s.H.Store.InWorkspace(context.Background(), wsID, ana, func(tx *store.Tx) error {
		var err error
		row, err = shipOne(context.Background(), tx, s.H.Blobs, s.H.Shipped, "demo")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows := s.versions(t, a, w.ID, "demo"); len(rows) != 1 || rows[0].ApprovalsByMe || rows[0].Source != "shipped" || rows[0].UploadedBy != nil {
		t.Fatalf("versions before approval %+v", rows)
	}
	for range 2 {
		if c := s.call(t, "POST", "/api/packs/"+row.ID.String()+"/approve", a, w.ID, nil, nil); c != 200 {
			t.Fatalf("approve: %d", c)
		}
	}
	rows := s.versions(t, a, w.ID, "demo")
	if len(rows) != 1 || !rows[0].ApprovalsByMe || rows[0].ApprovedAt == nil {
		t.Fatalf("versions %+v", rows)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'pack.approved' AND payload->>'pack_id' = $1", rows[0].ID); n != 1 {
		t.Fatalf("a repeated approval wrote %d events", n)
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
	if rows := s.versions(t, b, w.ID, "ana-pack"); len(rows) != 0 {
		t.Fatalf("Ben lists %+v", rows)
	}
	if c := s.call(t, "POST", "/api/packs/"+up.ID+"/approve", b, w.ID, nil, nil); c != 404 {
		t.Fatalf("Ben approved Ana's upload: %d", c)
	}
	if c := s.call(t, "POST", "/api/packs/"+up.ID+"/approve", a, w.ID, nil, nil); c != 200 {
		t.Fatalf("approve: %d", c)
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

func TestPackOtherWorkspace(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
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
		"skills/pi/work/a.bin": noise(), "skills/pi/work/b.bin": noise()})
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

// TestRoleRecordWithoutKinds checks a server with no kinds wired stores any
// well-formed kind as the run's work, keyed by the server.
func TestRoleRecordWithoutKinds(t *testing.T) {
	s := newServer(t)
	_, _, _, run := s.roleFixture(t)
	var rec struct{ Key string }
	if got := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "question", "body": map[string]string{"text": "Scoped?"}}, &rec); got != 201 || rec.Key != "r1" {
		t.Fatalf("%d %+v", got, rec)
	}
	if got := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "question", "key": "q1", "body": map[string]string{}}, nil); got != 400 {
		t.Fatalf("a key from the run: %d", got)
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

func TestRoleRecordsRefuseAnUnknownColumn(t *testing.T) {
	s := newServer(t)
	_, _, _, run := s.roleFixture(t)
	var e struct{ Error string }
	if got := s.call(t, "GET", "/api/role/records?column=literature", run.token, "", nil, &e); got != 400 || !strings.Contains(e.Error, "grounding, hypotheses") {
		t.Fatalf("unknown column: %d %+v", got, e)
	}
	if got := s.call(t, "GET", "/api/role/records?column=grounding", run.token, "", nil, nil); got != 200 {
		t.Fatalf("known column: %d", got)
	}
}
