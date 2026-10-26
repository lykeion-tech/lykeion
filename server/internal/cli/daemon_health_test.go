//go:build darwin || linux

package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDaemonJSONStatusNeverTrustsPIDAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", path)
	t.Setenv("LYK_DAEMON_PROFILE", "desktop-test")
	for _, live := range []bool{false, true} {
		if live {
			started, err := processStart(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			if err := writeOwner(filepath.Join(filepath.Dir(path), "daemon.pid"), daemonOwner{PID: os.Getpid(), Started: started, Nonce: "owned-but-no-health"}); err != nil {
				t.Fatal(err)
			}
		}
		out, err := commandResult("daemon", "status", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if got["status"] != "stopped" || got["profile"] != "desktop-test" {
			t.Fatalf("unverified status: %s", out)
		}
		if strings.Contains(out, "nonce") || strings.Contains(out, "token") {
			t.Fatalf("internal secret in diagnostics: %s", out)
		}
	}
}

func TestDaemonTextStatusRemainsCompatible(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	got, err := commandResult("daemon", "status")
	if err != nil || got != "not running\n" {
		t.Fatalf("text changed: %q %v", got, err)
	}
}

func TestDaemonJSONOmitsUnknownDiagnostics(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	out, err := commandResult("daemon", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"uptime", "workspace_count", "pid", "daemon_id", "device_name", "os"} {
		if _, exists := got[key]; exists {
			t.Fatalf("invented unknown %s: %s", key, out)
		}
	}
}

func TestDaemonJSONReportsOnlyVerifiedLiveDiagnostics(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", config)
	t.Setenv("LYK_DAEMON_PROFILE", "desktop-profile")
	started, err := processStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]any{"status": "running", "pid": os.Getpid(), "started": started, "nonce": "private-nonce", "uptime": 12.5, "daemon_id": "daemon-a", "profile": "desktop-profile", "server_url": "https://api.example", "device_name": "Ada", "workspace_count": 2, "os": runtime.GOOS}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" || r.Method != "GET" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("personal token sent to health listener")
		}
		_ = json.NewEncoder(w).Encode(live)
	}))
	defer srv.Close()
	_, portString, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"port": port, "pid": os.Getpid(), "started": started, "nonce": "private-nonce", "daemon_id": "daemon-a", "profile": "desktop-profile", "server_url": "https://api.example"}
	b, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(filepath.Dir(config), "daemon.health.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(config, Config{Server: "https://api.example", Token: "lyk_private"}); err != nil {
		t.Fatal(err)
	}
	out, err := commandResult("daemon", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"status", "pid", "uptime", "daemon_id", "profile", "server_url", "device_name", "workspace_count", "os"} {
		if fmt.Sprint(got[key]) != fmt.Sprint(live[key]) {
			t.Fatalf("%s mismatch: %s", key, out)
		}
	}
	for _, secret := range []string{"nonce", "started", "lyk_private"} {
		if strings.Contains(out, secret) {
			t.Fatalf("private field exposed: %s", out)
		}
	}
	t.Setenv("LYK_DAEMON_PROFILE", "other-profile")
	out, err = commandResult("daemon", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"stopped"`) {
		t.Fatalf("other profile adopted daemon: %s", out)
	}
}
