package handler

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lykeion/server/internal/service"
)

type roleRun struct {
	token, id string
	ref       map[string]any
	personal  string
}

func (s *server) running(t *testing.T, jwt, ws, cardID string) roleRun {
	t.Helper()
	personal := s.personalToken(t, jwt)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{"workspace_id": ws, "daemon_id": "d-" + cardID, "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register %d %+v", code, rts)
	}
	s.queue(t, ws, cardID, s.userID(t, jwt), "claude")
	ref := map[string]any{"workspace_id": ws, "runtime_id": rts[0].ID}
	var claims []struct {
		AgentRun struct {
			ID      string
			Attempt int32
		} `json:"agent_run"`
		Token string
	}
	if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", personal, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); code != 200 || len(claims) != 1 || claims[0].AgentRun.Attempt <= 0 {
		t.Fatalf("claim %d %+v", code, claims)
	}
	ref["attempt"] = claims[0].AgentRun.Attempt
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+claims[0].AgentRun.ID+"/start", personal, "", ref, nil); code != 204 {
		t.Fatalf("start %d", code)
	}
	return roleRun{claims[0].Token, claims[0].AgentRun.ID, ref, personal}
}
func (s *server) roleFixture(t *testing.T) (string, string, card, roleRun) {
	t.Helper()
	jwt := s.signIn(t, "role@lab.org")
	w := s.workspace(t, jwt, "lab")
	var c card
	if code := s.call(t, "POST", "/api/cards", jwt, w.ID, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatalf("card %d", code)
	}
	return jwt, w.ID, c, s.running(t, jwt, w.ID, c.ID)
}
func (s *server) blobRequest(t *testing.T, method, path, token string, body io.Reader) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}
func TestRoleRecords(t *testing.T) {
	s := newServer(t)
	s.H.Kinds = map[string]service.Kind{"question": {Roles: []string{"scoper"}, Validate: func(b json.RawMessage) error {
		var v struct{ Text string }
		if json.Unmarshal(b, &v) != nil || v.Text == "" {
			return errors.New("text required")
		}
		return nil
	}}, "evidence": {Roles: []string{"literature-scout"}}, "free": {Roles: []string{"scoper"}}}
	jwt, ws, c, run := s.roleFixture(t)
	valid := map[string]any{"kind": "question", "key": "q1", "body": map[string]string{"text": "Scoped?"}}
	var rec map[string]any
	if code := s.call(t, "POST", "/api/role/records", run.token, "", valid, &rec); code != 201 {
		t.Fatalf("own kind: %d", code)
	}
	if rec["card_id"] != c.ID || rec["workspace_id"] != ws || rec["agent_run_id"] != run.id || rec["role"] != "scoper" || rec["board_column"] != "grounding" || rec["round"] != float64(1) {
		t.Fatalf("token scope %+v", rec)
	}
	for _, tc := range []struct {
		kind string
		body any
		want int
	}{{"evidence", map[string]any{}, 403}, {"question", map[string]any{}, 400}, {"nonsense", map[string]any{}, 400}, {"free", nil, 400}, {"free", []string{}, 400}, {"free", "x", 400}, {"free", map[string]any{}, 201}} {
		if got := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": tc.kind, "body": tc.body}, nil); got != tc.want {
			t.Fatalf("%+v: %d", tc, got)
		}
	}
	for _, field := range []string{"workspace_id", "card_id", "agent_run_id", "role", "round", "column"} {
		in := map[string]any{"kind": "free", "body": map[string]any{}, field: c.ID}
		if got := s.call(t, "POST", "/api/role/records", run.token, "", in, nil); got != 400 {
			t.Fatalf("override %s: %d", field, got)
		}
	}
	var recs []map[string]any
	if got := s.call(t, "GET", "/api/role/records", run.token, "", nil, &recs); got != 200 || len(recs) != 2 {
		t.Fatalf("records %d %+v", got, recs)
	}
	if got := s.call(t, "GET", "/api/role/records?column=other", run.token, "", nil, &recs); got != 200 || len(recs) != 0 {
		t.Fatalf("column %d %+v", got, recs)
	}
	for _, tok := range []string{jwt, run.personal, "ltk_invalid", ""} {
		if got := s.call(t, "GET", "/api/role/records", tok, "", nil, nil); got != 401 {
			t.Fatalf("non-run credential: %d", got)
		}
	}
	if got := s.call(t, "GET", "/api/cards", run.token, ws, nil, nil); got != 401 {
		t.Fatalf("people route: %d", got)
	}
	var c2 card
	if got := s.call(t, "POST", "/api/cards", jwt, ws, map[string]any{"question": "q2"}, &c2); got != 201 {
		t.Fatal(got)
	}
	other := s.running(t, jwt, ws, c2.ID)
	if got := s.call(t, "GET", "/api/role/records?card_id="+c.ID, other.token, "", nil, &recs); got != 200 || len(recs) != 0 {
		t.Fatalf("cross-card list %d %+v", got, recs)
	}
	if got := s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/complete", run.personal, "", run.ref, nil); got != 200 {
		t.Fatal(got)
	}
	if got := s.call(t, "POST", "/api/role/records", run.token, "", valid, nil); got != 401 {
		t.Fatalf("settled write %d", got)
	}
}
func TestRoleBlobs(t *testing.T) {
	s := newServer(t)
	s.H.Kinds = map[string]service.Kind{"free": {Roles: []string{"scoper"}}}
	jwt, ws, c, run := s.roleFixture(t)
	data := bytes.Repeat([]byte("blob"), 400000)
	code, b := s.blobRequest(t, "POST", "/api/role/blobs", run.token, bytes.NewReader(data))
	if code != 201 {
		t.Fatalf("valid >1MiB upload: %d %s", code, b)
	}
	var blob struct {
		Key  string `json:"blob_key"`
		SHA  string `json:"sha256"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(b, &blob); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if blob.Size != int64(len(data)) || blob.SHA != hex.EncodeToString(sum[:]) || blob.Key != ws+"/"+c.ID+"/"+blob.SHA {
		t.Fatalf("blob %+v", blob)
	}
	code, b = s.blobRequest(t, "GET", "/api/role/blobs/"+blob.SHA, run.token, nil)
	if code != 200 || !bytes.Equal(b, data) {
		t.Fatalf("read %d %d", code, len(b))
	}
	in := map[string]any{"kind": "free", "body": map[string]any{}, "blob_key": blob.Key}
	if code := s.call(t, "POST", "/api/role/records", run.token, "", in, nil); code != 201 {
		t.Fatalf("reference %d", code)
	}
	var c2 card
	if code := s.call(t, "POST", "/api/cards", jwt, ws, map[string]string{"question": "q2"}, &c2); code != 201 {
		t.Fatal(code)
	}
	other := s.running(t, jwt, ws, c2.ID)
	if code, _ := s.blobRequest(t, "GET", "/api/role/blobs/"+blob.SHA, other.token, nil); code != 404 {
		t.Fatalf("cross-card read %d", code)
	}
	if code := s.call(t, "POST", "/api/role/records", other.token, "", in, nil); code != 400 {
		t.Fatalf("cross-card write %d", code)
	}
	for _, key := range []string{ws + "/" + c.ID + "/..", ws + "/" + c.ID + "/" + strings.Repeat("g", 64), ws + "/" + c.ID + "/" + strings.Repeat("0", 64), blob.Key + "/extra"} {
		in["blob_key"] = key
		if code := s.call(t, "POST", "/api/role/records", run.token, "", in, nil); code != 400 {
			t.Fatalf("bad key %s: %d", key, code)
		}
	}
	for _, sha := range []string{"bad", strings.Repeat("G", 64)} {
		if code, _ := s.blobRequest(t, "GET", "/api/role/blobs/"+sha, run.token, nil); code != 400 {
			t.Fatalf("bad sha %d", code)
		}
	}
	if code, b := s.blobRequest(t, "POST", "/api/role/blobs", run.token, io.LimitReader(zeroReader{}, 50<<20)); code != 201 {
		t.Fatalf("exact 50MiB: %d %s", code, b)
	} else {
		var out struct {
			Size int64 `json:"size"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if out.Size != 50<<20 {
			t.Fatalf("boundary size %d", out.Size)
		}
	}
	if code, _ := s.blobRequest(t, "POST", "/api/role/blobs", run.token, io.LimitReader(zeroReader{}, (50<<20)+1)); code != 413 {
		t.Fatalf("oversize %d", code)
	}
	if code := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "free", "body": map[string]string{"text": strings.Repeat("x", 1<<20)}}, nil); code != 413 {
		t.Fatalf("JSON limit %d", code)
	}
	var events int
	if err := s.Env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM event WHERE card_id=$1 AND type='blob.created' AND actor_type='role' AND actor_id=$2", c.ID, run.id).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("blob events %d", events)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestRoleWriteRevalidatesAfterSettlement(t *testing.T) {
	for _, action := range []string{"complete", "cancel", "revoke", "attempt", "expire"} {
		t.Run(action, func(t *testing.T) {
			s := newServer(t)
			jwt, ws, c, run := s.roleFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			s.H.Kinds = map[string]service.Kind{"free": {Roles: []string{"scoper"}, Validate: func(json.RawMessage) error { close(entered); <-release; return nil }}}
			result := make(chan int, 1)
			go func() {
				result <- s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "free", "body": map[string]any{}}, nil)
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("validator not reached")
			}
			if action == "complete" {
				if got := s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/complete", run.personal, "", run.ref, nil); got != 200 {
					t.Fatal(got)
				}
			} else if action == "cancel" {
				if got := s.call(t, "POST", "/api/agent-runs/"+run.id+"/cancel", jwt, ws, nil, nil); got != 204 {
					t.Fatal(got)
				}
			} else {
				query := "DELETE FROM agent_run_token WHERE agent_run_id=$1"
				if action == "attempt" {
					query = "UPDATE agent_run SET attempt=attempt+1 WHERE id=$1"
				}
				if action == "expire" {
					query = "UPDATE agent_run_token SET expires_at=now()-interval '1 minute' WHERE agent_run_id=$1"
				}
				tag, err := s.Env.Owner.Exec(context.Background(), query, run.id)
				if err != nil || tag.RowsAffected() != 1 {
					t.Fatalf("invalidate: %v %v", tag, err)
				}
			}
			unblock()
			if got := <-result; got != 401 {
				t.Fatalf("write after %s: %d", action, got)
			}
			var count int
			if err := s.Env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM record WHERE card_id=$1", c.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("late records %d", count)
			}
		})
	}
}
func TestRoleStaleAttemptRefused(t *testing.T) {
	s := newServer(t)
	_, _, _, run := s.roleFixture(t)
	tag, err := s.Env.Owner.Exec(context.Background(), "UPDATE agent_run SET attempt=attempt+1 WHERE id=$1", run.id)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("advance attempt %v %v", tag, err)
	}
	if got := s.call(t, "GET", "/api/role/records", run.token, "", nil, nil); got != 401 {
		t.Fatalf("stale attempt %d", got)
	}
}

// A deferred database failure happens after storage Put, at commit. The orphan
// must not be downloadable or attachable without its committed trace event.
func TestRoleBlobCommitFailureIsInaccessible(t *testing.T) {
	s := newServer(t)
	s.H.Kinds = map[string]service.Kind{"free": {Roles: []string{"scoper"}}}
	_, ws, c, run := s.roleFixture(t)
	_, err := s.Env.Owner.Exec(context.Background(), `CREATE FUNCTION reject_blob_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='blob.created' THEN RAISE EXCEPTION 'injected blob commit failure'; END IF; RETURN NEW; END $$;
 CREATE CONSTRAINT TRIGGER reject_blob_commit AFTER INSERT ON event DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_blob_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("orphan")
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	key := ws + "/" + c.ID + "/" + sha
	if code, _ := s.blobRequest(t, "POST", "/api/role/blobs", run.token, bytes.NewReader(data)); code != 500 {
		t.Fatalf("commit error %d", code)
	}
	rc, err := s.H.Blobs.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("storage write did not precede failed commit: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(context.Background(), "DROP TRIGGER reject_blob_commit ON event"); err != nil {
		t.Fatal(err)
	}
	if code, _ := s.blobRequest(t, "GET", "/api/role/blobs/"+sha, run.token, nil); code != 404 {
		t.Fatalf("orphan read %d", code)
	}
	if code := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "free", "body": map[string]any{}, "blob_key": key}, nil); code != 400 {
		t.Fatalf("orphan reference %d", code)
	}
	var count int
	if err := s.Env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM event WHERE card_id=$1 AND type='blob.created'", c.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back events %d", count)
	}
}

// Blocking only the external storage call leaves the real database lock and
// settlement code in place, so this detects releasing authority before Put.
type heldStorage struct {
	storage.Storage
	entered chan struct{}
	release chan struct{}
}

func (s heldStorage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Storage.Put(ctx, key, r, size)
}
func TestRoleBlobWriteSerializesWithSettlement(t *testing.T) {
	s := newServer(t)
	_, _, c, run := s.roleFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	s.H.Blobs = heldStorage{s.H.Blobs, entered, release}
	upload := make(chan int, 1)
	go func() {
		code, _ := s.blobRequest(t, "POST", "/api/role/blobs", run.token, strings.NewReader("serialized"))
		upload <- code
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("storage not reached")
	}
	done := make(chan int, 1)
	go func() {
		done <- s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/complete", run.personal, "", run.ref, nil)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked int
		err := s.Env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockAgentRun%'`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("settlement passed active blob write: %d", code)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("settlement did not wait for run lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	unblock()
	if code := <-upload; code != 201 {
		t.Fatalf("upload %d", code)
	}
	if code := <-done; code != 200 {
		t.Fatalf("settle %d", code)
	}
	var blobEvent, settleEvent int64
	if err := s.Env.Owner.QueryRow(context.Background(), "SELECT id FROM event WHERE card_id=$1 AND type='blob.created'", c.ID).Scan(&blobEvent); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(context.Background(), "SELECT id FROM event WHERE card_id=$1 AND type='agent_run.completed'", c.ID).Scan(&settleEvent); err != nil {
		t.Fatal(err)
	}
	if blobEvent >= settleEvent {
		t.Fatalf("event order: blob=%d settlement=%d", blobEvent, settleEvent)
	}
}

// An authenticated upload still spooling must not retain a tenant transaction
// and must be refused if its run settles before the complete body arrives.
func TestRoleBlobSpoolsBeforeTransactionAndRevalidates(t *testing.T) {
	s := newServer(t)
	_, _, c, run := s.roleFixture(t)
	body := &heldBody{entered: make(chan struct{}), release: make(chan struct{}), Reader: strings.NewReader("late upload")}
	defer body.Close()
	req := httptest.NewRequest("POST", "/api/role/blobs", body)
	req.Header.Set("Authorization", "Bearer "+run.token)
	out := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { NewRouter(s.H).ServeHTTP(out, req); close(done) }()
	select {
	case <-body.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("spool not reached")
	}
	if n := s.Env.App.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("spool holds %d DB connections", n)
	}
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/complete", run.personal, "", run.ref, nil); code != 200 {
		t.Fatal(code)
	}
	body.Close()
	<-done
	if out.Code != 401 {
		t.Fatalf("late upload %d %s", out.Code, out.Body.String())
	}
	var count int
	if err := s.Env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM event WHERE card_id=$1 AND type='blob.created'", c.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("late blob events %d", count)
	}
}

type heldBody struct {
	io.Reader
	entered, release chan struct{}
	start, finish    sync.Once
}

func (b *heldBody) Read(p []byte) (int, error) {
	b.start.Do(func() { close(b.entered) })
	<-b.release
	return b.Reader.Read(p)
}
func (b *heldBody) Close() error { b.finish.Do(func() { close(b.release) }); return nil }

func TestRoleSupersedeRetainsHistoricalRows(t *testing.T) {
	s := newServer(t)
	s.H.Kinds = map[string]service.Kind{"free": {Roles: []string{"scoper"}}}
	_, ws, c, run := s.roleFixture(t)
	in := map[string]any{"kind": "free", "key": "same", "body": map[string]any{}}
	if code := s.call(t, "POST", "/api/role/records", run.token, "", in, nil); code != 201 {
		t.Fatal(code)
	}
	err := s.H.Store.InWorkspace(context.Background(), uuid.MustParse(ws), uuid.MustParse(run.id), func(tx *store.Tx) error {
		return service.SupersedeRole(context.Background(), tx, uuid.MustParse(c.ID), "grounding", "scoper")
	})
	if err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/role/records", run.token, "", in, nil); code != 201 {
		t.Fatalf("replacement %d", code)
	}
	var rows []map[string]any
	if code := s.call(t, "GET", "/api/role/records", run.token, "", nil, &rows); code != 200 || len(rows) != 1 {
		t.Fatalf("live records %d %+v", code, rows)
	}
	var total, retired int
	if err := s.Env.Owner.QueryRow(context.Background(), "SELECT count(*),count(superseded_at) FROM record WHERE card_id=$1", c.ID).Scan(&total, &retired); err != nil {
		t.Fatal(err)
	}
	if total != 2 || retired != 1 {
		t.Fatalf("history %d retired %d", total, retired)
	}
}

func TestRoleBlobAuthenticatesWithoutReadingBody(t *testing.T) {
	s := newServer(t)
	jwt, _, _, run := s.roleFixture(t)
	for _, token := range []string{jwt, run.personal, "ltk_bad"} {
		body := &unreadableBody{}
		req := httptest.NewRequest("POST", "/api/role/blobs", body)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		NewRouter(s.H).ServeHTTP(out, req)
		if out.Code != 401 || body.read {
			t.Fatalf("unauthenticated body read=%v status=%d", body.read, out.Code)
		}
	}
}

type unreadableBody struct{ read bool }

func (b *unreadableBody) Read([]byte) (int, error) {
	b.read = true
	return 0, errors.New("broken upload")
}

func TestRoleBlobRejectsIncompleteUnauthorizedUploadPromptly(t *testing.T) {
	s := newServer(t)
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(s.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// A short, unfinished body must not be drained after the authentication failure.
	if _, err := io.WriteString(conn, "POST /api/role/blobs HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer ltk_bad\r\nContent-Length: 1024\r\n\r\nx"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("authentication waited for body: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated %d", resp.StatusCode)
	}
}

func TestRoleBlobIOErrorsAndMissingBytes(t *testing.T) {
	s := newServer(t)
	s.H.Kinds = map[string]service.Kind{"free": {Roles: []string{"scoper"}}}
	_, _, c, run := s.roleFixture(t)
	req := httptest.NewRequest("POST", "/api/role/blobs", &unreadableBody{})
	req.Header.Set("Authorization", "Bearer "+run.token)
	out := httptest.NewRecorder()
	NewRouter(s.H).ServeHTTP(out, req)
	if out.Code != 400 {
		t.Fatalf("body error %d", out.Code)
	}
	disk := s.H.Blobs.(storage.Local)
	s.H.Blobs = storage.Local{Dir: filepath.Join(disk.Dir, "not-a-directory")}
	if err := os.WriteFile(filepath.Join(disk.Dir, "not-a-directory"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _ := s.blobRequest(t, "POST", "/api/role/blobs", run.token, strings.NewReader("cannot store")); code != 500 {
		t.Fatalf("storage error %d", code)
	}
	var events int
	if err := s.Env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM event WHERE card_id=$1 AND type='blob.created'", c.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("failed upload events %d", events)
	}
	s.H.Blobs = disk
	code, b := s.blobRequest(t, "POST", "/api/role/blobs", run.token, strings.NewReader("remove me"))
	if code != 201 {
		t.Fatal(code)
	}
	var blob struct {
		Key string `json:"blob_key"`
		SHA string `json:"sha256"`
	}
	if err := json.Unmarshal(b, &blob); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(disk.Dir, blob.Key)); err != nil {
		t.Fatal(err)
	}
	if code, _ := s.blobRequest(t, "GET", "/api/role/blobs/"+blob.SHA, run.token, nil); code != 404 {
		t.Fatalf("missing bytes %d", code)
	}
	if code := s.call(t, "POST", "/api/role/records", run.token, "", map[string]any{"kind": "free", "body": map[string]any{}, "blob_key": blob.Key}, nil); code != 400 {
		t.Fatalf("missing-byte reference %d", code)
	}
}

func TestRoleBlobReadHonorsEarlierRequestDeadline(t *testing.T) {
	s := newServer(t)
	_, _, _, run := s.roleFixture(t)
	router := NewRouter(s.H)
	bounded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
		defer cancel()
		router.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer bounded.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(bounded.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /api/role/blobs HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nContent-Length: 1024\r\n\r\nx", run.token); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("body read not interrupted: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 408 {
		t.Fatalf("stalled upload %d", resp.StatusCode)
	}
}
