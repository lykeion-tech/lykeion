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
	"syscall"
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

func TestDaemonJSONCannotCallAnOrphanStartupStopped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LYK_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("LYK_DAEMON_PROFILE", "desktop-orphan")
	state := filepath.Join(dir, "daemon")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "daemon.lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, health := range []string{"", `{"port":1,"pid":1,"started":"stale","nonce":"wrong","profile":"other","server_url":"https://other","daemon_id":"other"}`} {
		if health != "" {
			if err := os.WriteFile(filepath.Join(dir, "daemon.health.json"), []byte(health), 0600); err != nil {
				t.Fatal(err)
			}
		}
		out, err := commandResult("daemon", "status", "--json")
		if err == nil || out != "" {
			t.Fatalf("live lock reported stopped: %s %v", out, err)
		}
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("lock inode changed: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	out, err := commandResult("daemon", "status", "--json")
	if err != nil || !strings.Contains(out, `"status":"stopped"`) {
		t.Fatalf("free lock not stopped: %s %v", out, err)
	}
}

func TestDaemonJSONRejectsUnsafeStateLocksWithoutChangingThem(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "state-symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LYK_CONFIG", filepath.Join(dir, "config.json"))
			state := filepath.Join(dir, "daemon")
			if kind == "state-symlink" {
				if err := os.Symlink(t.TempDir(), state); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(state, "daemon.lock")
				if kind == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					target := filepath.Join(t.TempDir(), "lock")
					if err := os.WriteFile(target, nil, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			out, err := commandResult("daemon", "status", "--json")
			if err == nil || out != "" {
				t.Fatalf("unsafe lock trusted: %s %v", out, err)
			}
		})
	}
}

func TestDaemonJSONDoesNotCreateMissingStateLocks(t *testing.T) {
	for _, directoryExists := range []bool{false, true} {
		t.Run(fmt.Sprint(directoryExists), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LYK_CONFIG", filepath.Join(dir, "config.json"))
			state := filepath.Join(dir, "daemon")
			if directoryExists {
				if err := os.Mkdir(state, 0755); err != nil {
					t.Fatal(err)
				}
			}
			out, err := commandResult("daemon", "status", "--json")
			if err != nil || !strings.Contains(out, `"status":"stopped"`) {
				t.Fatalf("absent lock: %s %v", out, err)
			}
			if _, err := os.Lstat(filepath.Join(state, "daemon.lock")); !os.IsNotExist(err) {
				t.Fatalf("status created lock: %v", err)
			}
			info, err := os.Lstat(state)
			if directoryExists {
				if err != nil || info.Mode().Perm() != 0755 {
					t.Fatalf("state mode changed: %v %v", info, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("status created state directory: %v", err)
			}
		})
	}
}
