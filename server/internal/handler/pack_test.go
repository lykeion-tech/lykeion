package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
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

// installPackFixture seeds a pre-existing private Team version for API tests.
// It bypasses HTTP so fixtures cannot make a removed endpoint appear usable.
func (s *server) installPackFixture(t *testing.T, tok, wsID string, z []byte, out any) {
	t.Helper()
	ctx := t.Context()
	ws, user := uuid.MustParse(wsID), uuid.MustParse(s.userID(t, tok))
	p, err := pack.Parse(z)
	if err != nil {
		t.Fatal(err)
	}
	key, err := pack.PutBlob(ctx, s.H.Blobs, ws, z, p.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	var row db.FieldPack
	err = s.H.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		var err error
		row, err = pack.Insert(ctx, tx, key, p, &user)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		body, err := json.Marshal(map[string]any{"id": row.ID, "name": row.Name, "version": row.Version, "sha256": row.Sha256})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *server) versions(t *testing.T, tok, wsID, name string) []packRow {
	t.Helper()
	var rows []packRow
	if c := s.call(t, "GET", "/api/packs?name="+name, tok, wsID, nil, &rows); c != 200 {
		t.Fatalf("versions: %d", c)
	}
	return rows
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
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "demo", Version: "1", Description: "Demo field"}))
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

func TestPackArchiveUploadIsRemoved(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	req, err := http.NewRequest("POST", s.URL+"/api/packs", bytes.NewReader(testutil.Pack(t, testutil.PackOpts{Name: "removed"})))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a)
	req.Header.Set("X-Workspace-ID", w.ID)
	req.Header.Set("Content-Type", "application/zip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("archive upload: %d", resp.StatusCode)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 {
		t.Fatalf("stored %d packs", n)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'pack.uploaded'"); n != 0 {
		t.Fatalf("emitted %d upload events", n)
	}
}

func TestInstalledPrivatePackIsVisibleOnlyToItsOwner(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "ana@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "ben@lab.org", "member")
	var up struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "ana-pack"}), &up)
	if rows := s.versions(t, a, w.ID, "ana-pack"); len(rows) != 1 {
		t.Fatalf("owner versions: %+v", rows)
	}
	if rows := s.versions(t, b, w.ID, "ana-pack"); len(rows) != 0 {
		t.Fatalf("colleague versions: %+v", rows)
	}
	if code := s.call(t, "POST", "/api/packs/"+up.ID+"/approve", b, w.ID, nil, nil); code != 404 {
		t.Fatalf("colleague approval: %d", code)
	}
	if code := s.call(t, "POST", "/api/packs/"+up.ID+"/approve", a, w.ID, nil, nil); code != 200 {
		t.Fatalf("owner approval: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type = 'pack.approved' AND payload - 'pack_id' <> '{}'::jsonb"); n != 0 {
		t.Fatalf("%d private approval events exposed metadata", n)
	}
}

func TestPackOtherWorkspace(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	var mine struct{ ID string }
	s.installPackFixture(t, a, wa.ID, testutil.Pack(t, testutil.PackOpts{Name: "own"}), &mine)
	if c := s.call(t, "POST", "/api/packs/"+mine.ID+"/approve", b, wb.ID, nil, nil); c != 404 {
		t.Fatalf("approved another workspace's upload: %d", c)
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
	c := s.idleCard(t, a, w.ID, "q")
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
	c := s.idleCard(t, a, w.ID, "q")
	other := s.idleCard(t, a, w.ID, "q2")
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
