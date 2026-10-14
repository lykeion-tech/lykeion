package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/testutil"
)

func config(t *testing.T, env *testutil.Env) map[string]string {
	t.Helper()
	return map[string]string{"DATABASE_URL": env.AppURL, "MIGRATE_DATABASE_URL": env.OwnerURL, "REDIS_URL": env.RedisURL, "JWT_SECRET": "0123456789abcdef0123456789abcdef", "LISTEN_ADDR": "127.0.0.1:0", "COOKIE_SECURE": "false", "STORAGE": "local:" + t.TempDir()}
}

func TestStorageSetting(t *testing.T) {
	if _, err := newStorage(context.Background(), func(k string) string { return map[string]string{"STORAGE": "local:" + t.TempDir()}[k] }); err != nil {
		t.Fatal(err)
	}
	if _, err := newStorage(context.Background(), func(string) string { return "" }); err == nil {
		t.Fatal("no STORAGE setting was accepted")
	}
	for _, setting := range []string{"local:", "local:relative", "local:" + filepath.Join(t.TempDir(), "file"), "unknown"} {
		if strings.HasSuffix(setting, "file") {
			if err := os.WriteFile(strings.TrimPrefix(setting, "local:"), []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := newStorage(context.Background(), func(k string) string {
			if k == "STORAGE" {
				return setting
			}
			return ""
		}); err == nil {
			t.Fatalf("invalid STORAGE accepted: %q", setting)
		}
	}
	for _, values := range []map[string]string{
		{"STORAGE": "s3"},
		{"STORAGE": "s3", "S3_BUCKET": "bucket"},
		{"STORAGE": "s3", "S3_BUCKET": "bucket", "S3_REGION": "us-east-1", "S3_ENDPOINT": "not-a-url"},
	} {
		if _, err := newStorage(context.Background(), func(k string) string { return values[k] }); err == nil {
			t.Fatalf("invalid S3 configuration accepted: %+v", values)
		}
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test-only-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-only-secret")
	validS3 := map[string]string{"STORAGE": "s3", "S3_BUCKET": "test-bucket", "S3_REGION": "us-east-1", "S3_ENDPOINT": "http://127.0.0.1:9000"}
	blobs, err := newStorage(context.Background(), func(k string) string { return validS3[k] })
	if err != nil {
		t.Fatalf("valid S3 settings: %v", err)
	}
	if _, ok := blobs.(*storage.S3); !ok {
		t.Fatalf("STORAGE=s3 selected %T", blobs)
	}
}

func TestServerRejectsMissingStorageBeforeReady(t *testing.T) {
	env := testutil.Setup(t)
	values := config(t, env)
	delete(values, "STORAGE")
	ready := make(chan string, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := run(ctx, func(k string) string { return values[k] }, ready); err == nil || !strings.Contains(err.Error(), "STORAGE") {
		t.Fatalf("missing storage was accepted: %v", err)
	}
	select {
	case addr := <-ready:
		t.Fatalf("announced ready without storage: %s", addr)
	default:
	}
}
func start(t *testing.T, values map[string]string) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return values[k] }, ready) }()
	select {
	case addr := <-ready:
		return addr, cancel, done
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("startup timed out")
	}
	return "", cancel, done
}
func stopped(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown stalled")
	}
}
func TestServerStartsMigratedAndReady(t *testing.T) {
	env := testutil.Setup(t)
	if _, err := env.Owner.Exec(context.Background(), "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	addr, cancel, done := start(t, config(t, env))
	resp, err := http.Get("http://" + addr + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("readyz: %d", resp.StatusCode)
	}
	var count int
	if err := env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count == 0 {
		t.Fatalf("migrations: %d %v", count, err)
	}
	cancel()
	stopped(t, done)
}
func TestServerRefusesWeakSecret(t *testing.T) {
	err := run(context.Background(), func(k string) string {
		if k == "JWT_SECRET" {
			return "short"
		}
		return ""
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("weak secret: %v", err)
	}
}

func TestServerRejectsInvalidConfiguration(t *testing.T) {
	for _, key := range []string{"JWT_SECRET", "DATABASE_URL", "MIGRATE_DATABASE_URL", "REDIS_URL"} {
		t.Run(key, func(t *testing.T) {
			values := map[string]string{"JWT_SECRET": "0123456789abcdef0123456789abcdef", "DATABASE_URL": "x", "MIGRATE_DATABASE_URL": "x", "REDIS_URL": "x"}
			delete(values, key)
			if err := run(context.Background(), func(k string) string { return values[k] }, nil); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("missing %s: %v", key, err)
			}
		})
	}
}
func TestServerRejectsUnsafeDatabaseRoles(t *testing.T) {
	env := testutil.Setup(t)
	for _, mode := range []string{"owner", "wrong-role", "superuser", "bypassrls", "table-owner"} {
		t.Run(mode, func(t *testing.T) {
			values := config(t, env)
			switch mode {
			case "owner":
				values["DATABASE_URL"] = env.OwnerURL
			case "wrong-role":
				if _, err := env.Owner.Exec(context.Background(), "CREATE ROLE unsafeapp LOGIN PASSWORD 'unsafe'"); err != nil {
					t.Fatal(err)
				}
				defer env.Owner.Exec(context.Background(), "DROP ROLE unsafeapp")
				u, _ := url.Parse(env.AppURL)
				u.User = url.UserPassword("unsafeapp", "unsafe")
				values["DATABASE_URL"] = u.String()
			case "superuser":
				if _, err := env.Owner.Exec(context.Background(), "ALTER ROLE lykeion_app SUPERUSER"); err != nil {
					t.Fatal(err)
				}
				defer env.Owner.Exec(context.Background(), "ALTER ROLE lykeion_app NOSUPERUSER")
			case "bypassrls":
				if _, err := env.Owner.Exec(context.Background(), "ALTER ROLE lykeion_app BYPASSRLS"); err != nil {
					t.Fatal(err)
				}
				defer env.Owner.Exec(context.Background(), "ALTER ROLE lykeion_app NOBYPASSRLS")
			case "table-owner":
				if _, err := env.Owner.Exec(context.Background(), "ALTER TABLE card OWNER TO lykeion_app"); err != nil {
					t.Fatal(err)
				}
				defer env.Owner.Exec(context.Background(), "ALTER TABLE card OWNER TO owner")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ready := make(chan string, 1)
			err := run(ctx, func(k string) string { return values[k] }, ready)
			if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
				t.Fatalf("unsafe role accepted or wrong error: %v", err)
			}
			select {
			case addr := <-ready:
				t.Fatalf("unsafe startup announced ready: %s", addr)
			default:
			}
		})
	}
}
func TestServerRejectsUnavailableDependenciesBeforeReady(t *testing.T) {
	env := testutil.Setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	for _, key := range []string{"DATABASE_URL", "REDIS_URL"} {
		t.Run(key, func(t *testing.T) {
			values := config(t, env)
			u, _ := url.Parse(values[key])
			u.Host = addr
			values[key] = u.String()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			ready := make(chan string, 1)
			if err := run(ctx, func(k string) string { return values[k] }, ready); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("unavailable %s: %v", key, err)
			}
			select {
			case addr := <-ready:
				t.Fatalf("announced ready: %s", addr)
			default:
			}
		})
	}
}
func TestServerCancellationClosesWebSocket(t *testing.T) {
	env := testutil.Setup(t)
	values := config(t, env)
	id := uuid.New()
	if _, err := env.Owner.Exec(context.Background(), `INSERT INTO "user" (id,email,name) VALUES ($1,'socket@example.org','socket')`, id); err != nil {
		t.Fatal(err)
	}
	tok, err := (auth.Issuer{Secret: []byte(values["JWT_SECRET"]), TTL: time.Hour}).Issue(id, "socket@example.org")
	if err != nil {
		t.Fatal(err)
	}
	addr, cancel, done := start(t, values)
	req, err := http.NewRequest("POST", "http://"+addr+"/api/workspaces", strings.NewReader(`{"slug":"socket-lab","name":"Socket lab"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var ws struct{ ID string }
	err = json.NewDecoder(resp.Body).Decode(&ws)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("workspace: %d %v", resp.StatusCode, err)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "workspace": ws.ID, "cursor": 0}); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var message struct {
		Type  string
		Event struct{ Type string }
	}
	if err := conn.ReadJSON(&message); err != nil || message.Event.Type != "workspace.created" {
		t.Fatalf("active subscription: %+v %v", message, err)
	}
	cancel()
	stopped(t, done)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("socket remained open")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("socket not closed on shutdown")
	}
}
func TestServerReadyDeliveryIsBounded(t *testing.T) {
	env := testutil.Setup(t)
	values := config(t, env)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return values[k] }, make(chan string)) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "ready") {
			t.Fatalf("ready timeout: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unread ready channel stalled startup")
	}
}

func TestServerCancellationDuringSMTPReleasesPoolAndRollsBack(t *testing.T) {
	env := testutil.Setup(t)
	values := config(t, env)
	user, ws := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(t.Context(), `INSERT INTO "user" (id,email,name) VALUES ($1,'owner@lab.org','owner')`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(t.Context(), `INSERT INTO workspace (id,slug,name) VALUES ($1,'shutdown-mail','mail')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(t.Context(), `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, user); err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Issuer{Secret: []byte(values["JWT_SECRET"]), TTL: time.Hour}).Issue(user, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	values["SMTP_ADDR"], values["SMTP_FROM"] = ln.Addr().String(), "owner@lab.org"
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	addr, cancel, done := start(t, values)
	req, err := http.NewRequest("POST", "http://"+addr+"/api/workspaces/"+ws.String()+"/invitations", strings.NewReader(`{"email":"guest@lab.org","role":"member"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		response <- err
	}()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("SMTP not connected")
	}
	defer conn.Close()
	cancel()
	stopped(t, done)
	select {
	case <-response:
	case <-time.After(time.Second):
		t.Fatal("HTTP request retained")
	}
	for _, table := range []string{"workspace_invitation", "event"} {
		var n int
		if err := env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE workspace_id=$1", ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s committed during SMTP cancellation: %d", table, n)
		}
	}
}
