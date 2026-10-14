package main

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/testutil"
)

func config(env *testutil.Env) map[string]string {
	return map[string]string{"DATABASE_URL": env.AppURL, "MIGRATE_DATABASE_URL": env.OwnerURL, "REDIS_URL": env.RedisURL, "JWT_SECRET": "0123456789abcdef0123456789abcdef", "LISTEN_ADDR": "127.0.0.1:0", "COOKIE_SECURE": "false"}
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
	addr, cancel, done := start(t, config(env))
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
			values := config(env)
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
			values := config(env)
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
	values := config(env)
	id := uuid.New()
	if _, err := env.Owner.Exec(context.Background(), `INSERT INTO "user" (id,email,name) VALUES ($1,'socket@example.org','socket')`, id); err != nil {
		t.Fatal(err)
	}
	tok, err := (auth.Issuer{Secret: []byte(values["JWT_SECRET"]), TTL: time.Hour}).Issue(id, "socket@example.org")
	if err != nil {
		t.Fatal(err)
	}
	addr, cancel, done := start(t, values)
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
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
	values := config(env)
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
