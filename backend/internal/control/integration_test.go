package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Server, *store.Store, string, string, string, string) {
	t.Helper()
	dsn := os.Getenv("LYKEION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL URL not configured")
	}
	ctx := context.Background()
	admin, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := store.ID("test")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	config, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, e := store.Open(ctx, dsn+sep+"search_path="+schema)
	if e != nil {
		t.Fatal(e)
	}
	_ = config
	t.Cleanup(func() { db.Close(); admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close(ctx) })
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = db.Migrate(ctx); e != nil {
		t.Fatal("migration replay", e)
	}
	user, w, code, e := db.Provision(ctx, "Test scientist", "Test programme workspace")
	if e != nil {
		t.Fatal(e)
	}
	token, session, e := db.Exchange(ctx, code)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = db.Exchange(ctx, code); e == nil {
		t.Fatal("login code reused")
	}
	api, e := New(db, []byte(strings.Repeat("k", 32)), []string{"http://127.0.0.1:1430"}, false)
	if e != nil {
		t.Fatal(e)
	}
	return api, db, user, w, token, session.CSRF
}
func request(t *testing.T, client *http.Client, method, url, token, csrf, key string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r, e := http.NewRequest(method, url, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Origin", "http://127.0.0.1:1430")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Idempotency-Key", key)
	r.AddCookie(&http.Cookie{Name: "lykeion_session", Value: token})
	res, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}
func preference(w, value string) map[string]any {
	return map[string]any{"schema_version": "1", "target": map[string]any{"kind": "workspace", "id": w}, "expected_versions": []any{map[string]any{"kind": "workspace", "id": w, "revision": "1"}}, "payload": map[string]any{"preference_kind": "theme", "value": value}}
}
func TestCommandsAreDurableIdempotentAndScoped(t *testing.T) {
	api, db, user, w, token, csrf := fixture(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	path := server.URL + "/v1/workspaces/" + w + "/commands/preference.update"
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			status, b := request(t, server.Client(), "POST", path, token, csrf, "same-key", preference(w, "dark"))
			if status != 202 {
				t.Errorf("status=%d %s", status, b)
			}
		})
	}
	wg.Wait()
	var ops, events int
	db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM operations").Scan(&ops)
	db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM events").Scan(&events)
	if ops != 1 || events != 1 {
		t.Fatal("duplicate effects", ops, events)
	}
	status, _ := request(t, server.Client(), "POST", path, token, csrf, "same-key", preference(w, "light"))
	if status != 409 {
		t.Fatal("idempotency mismatch", status)
	}
	status, _ = request(t, server.Client(), "POST", path, token, "bad", "another", preference(w, "light"))
	if status != 403 {
		t.Fatal("CSRF", status)
	}
	_, other, _, e := db.Provision(context.Background(), "Other scientist", "Other workspace")
	if e != nil {
		t.Fatal(e)
	}
	status, _ = request(t, server.Client(), "GET", server.URL+"/v1/workspaces/"+other+"/members", token, csrf, "", nil)
	if status != 404 {
		t.Fatal("cross workspace", status)
	}
	c := api.signCursor(w, user, 1, 0)
	if _, e = api.parseCursor(c, w, "someone_else", 1); e == nil {
		t.Fatal("cursor scope")
	}
	if _, e = api.parseCursor(c, w, user, 2); e == nil {
		t.Fatal("cursor epoch")
	}
	status, b := request(t, server.Client(), "GET", server.URL+"/v1/workspaces/"+w+"/events?after="+c, token, csrf, "", nil)
	if status != 200 {
		t.Fatal(status, string(b))
	}
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	json.Unmarshal(b, &page)
	if len(page.Items) != 1 {
		t.Fatal(string(b))
	}
	// A second server instance reads the same committed log without an in-memory hub.
	second := httptest.NewServer(api.Handler())
	defer second.Close()
	status, b = request(t, second.Client(), "GET", second.URL+"/v1/workspaces/"+w+"/events?after="+c, token, csrf, "", nil)
	if status != 200 {
		t.Fatal(status, string(b))
	}
}
func TestWebSocketReconnectAndRevocation(t *testing.T) {
	api, db, user, w, token, csrf := fixture(t)
	first := httptest.NewServer(api.Handler())
	defer first.Close()
	second := httptest.NewServer(api.Handler())
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("Origin", "http://127.0.0.1:1430")
	headers.Set("Cookie", "lykeion_session="+token)
	ws, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(second.URL, "http")+"/v1/workspaces/"+w+"/realtime", &websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{"lykeion.workspace.v1"}})
	if e != nil {
		t.Fatal(e)
	}
	defer ws.CloseNow()
	raw, _ := json.Marshal(map[string]any{"schema_version": "1", "type": "subscribe", "message_id": "msg_test", "payload": map[string]any{"channels": []string{"workspace"}, "subject_refs": []any{}, "after_cursor": api.signCursor(w, user, 1, 0)}})
	if e = ws.Write(ctx, websocket.MessageText, raw); e != nil {
		t.Fatal(e)
	}
	_, raw, e = ws.Read(ctx)
	if e != nil || !bytes.Contains(raw, []byte("subscribed")) {
		t.Fatal(string(raw), e)
	}
	status, b := request(t, first.Client(), "POST", first.URL+"/v1/workspaces/"+w+"/commands/preference.update", token, csrf, "ws-command", preference(w, "dark"))
	if status != 202 {
		t.Fatal(status, string(b))
	}
	_, raw, e = ws.Read(ctx)
	if e != nil || !bytes.Contains(raw, []byte("preference.changed")) {
		t.Fatal(string(raw), e)
	}
	if _, e = db.Pool.Exec(ctx, "UPDATE memberships SET state='removed' WHERE workspace_id=$1 AND user_id=$2", w, user); e != nil {
		t.Fatal(e)
	}
	if _, _, e = ws.Read(ctx); e == nil {
		t.Fatal("removed member kept stream")
	}
	status, _ = request(t, first.Client(), "GET", first.URL+"/v1/workspaces/"+w+"/events", token, csrf, "", nil)
	if status != 404 {
		t.Fatal(status)
	}
}
func TestOriginAndSessionBoundaries(t *testing.T) {
	api, _, _, w, token, csrf := fixture(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	r, _ := http.NewRequest("GET", server.URL+"/v1/workspaces/"+w+"/members", nil)
	r.Header.Set("Origin", "https://untrusted.example")
	r.AddCookie(&http.Cookie{Name: "lykeion_session", Value: token})
	res, e := server.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("origin bypass")
	}
	status, b := request(t, server.Client(), "POST", server.URL+"/v1/auth/logout", token, csrf, "", nil)
	if status != 204 {
		t.Fatal(status, string(b))
	}
	status, _ = request(t, server.Client(), "GET", server.URL+"/v1/session", token, csrf, "", nil)
	if status != 401 {
		t.Fatal(fmt.Sprint("logout not durable: ", status))
	}
}
