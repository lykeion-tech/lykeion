package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/processidentity"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDesktopShutdownDoesNotDiscloseKeyToReplayedHealthEndpoint(t *testing.T) {
	started, err := processidentity.Start(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	var leaked atomic.Bool
	var shutdownAttempt atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), key) {
			leaked.Store(true)
		}
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "running", "pid": os.Getpid(), "started": started, "nonce": "public", "profile": "desktop-x", "server_url": "https://lab.example", "daemon_id": "x"})
			return
		}
		if r.URL.Path == "/shutdown-challenge" {
			_ = json.NewEncoder(w).Encode(map[string]string{"proof": shutdownProof(key, "server", strings.Repeat("b", 64), "public")})
			return
		}
		if r.URL.Path == "/shutdown" {
			shutdownAttempt.Store(true)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	_, rawPort, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "daemon.health.json")
	record := HealthRecord{Port: port, PID: os.Getpid(), Started: started, Nonce: "public", Profile: "desktop-x", ServerURL: "https://lab.example", DaemonID: "x", ShutdownKey: key}
	if err := writeHealthRecord(path, record); err != nil {
		t.Fatal(err)
	}
	if err := RequestDesktopShutdown(context.Background(), path, record.Profile, record.ServerURL, record.PID); err == nil {
		t.Fatal("accepted replayed health endpoint")
	}
	if leaked.Load() {
		t.Fatal("private shutdown key was disclosed to a replayed endpoint")
	}
	if shutdownAttempt.Load() {
		t.Fatal("sent shutdown proof to an endpoint replaying an old server proof")
	}
}

func TestHealthReportsLiveDiagnosticsAndRefusesMismatches(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process identity requires macOS/Linux")
	}
	path := filepath.Join(t.TempDir(), "daemon.health.json")
	d := &daemon{cfg: Config{Server: "https://lab.example", Token: "lyk_do_not_expose", Device: "Ada machine", Profile: "desktop-ada", HealthPath: path}, id: uuid.NewString(), knownWorkspaces: map[uuid.UUID]bool{uuid.New(): true}, workspaceCount: 1}
	closeHealth, err := d.startHealth()
	if err != nil {
		t.Fatal(err)
	}
	defer closeHealth()
	got, err := ProbeHealth(context.Background(), path, "desktop-ada", "https://lab.example/")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.PID != os.Getpid() || got.DaemonID != d.id || got.Profile != "desktop-ada" || got.ServerURL != d.cfg.Server || got.DeviceName != "Ada machine" || got.WorkspaceCount != 1 || got.OS != runtime.GOOS || got.Uptime < 0 {
		t.Fatalf("diagnostics: %+v", got)
	}
	d.mu.Lock()
	d.knownWorkspaces[uuid.New()] = true
	d.workspaceCount = 2
	d.mu.Unlock()
	got, err = ProbeHealth(context.Background(), path, "desktop-ada", d.cfg.Server)
	if err != nil || got.WorkspaceCount != 2 {
		t.Fatalf("not live: %+v %v", got, err)
	}
	for _, expected := range [][2]string{{"other", d.cfg.Server}, {"desktop-ada", "https://other.example"}} {
		if _, err := ProbeHealth(context.Background(), path, expected[0], expected[1]); err == nil {
			t.Fatal("accepted wrong configured identity")
		}
	}
	record, err := readHealthRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(record)
	for name, mutate := range map[string]func(*HealthRecord){
		"birth": func(r *HealthRecord) { r.Started = "reused" }, "nonce": func(r *HealthRecord) { r.Nonce = "wrong" }, "daemon": func(r *HealthRecord) { r.DaemonID = uuid.NewString() }, "profile": func(r *HealthRecord) { r.Profile = "other" }, "server": func(r *HealthRecord) { r.ServerURL = "https://other.example" }, "port": func(r *HealthRecord) { r.Port = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			altered := record
			mutate(&altered)
			b, _ := json.Marshal(altered)
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ProbeHealth(context.Background(), path, record.Profile, record.ServerURL); err == nil {
				t.Fatal("trusted mismatched record")
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
	client := &http.Client{Timeout: time.Second}
	for _, method := range []string{"POST", "DELETE"} {
		req, _ := http.NewRequest(method, healthURL(record.Port), nil)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("write method accepted: %d", response.StatusCode)
		}
	}

	response, err := client.Get(healthURL(record.Port))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "lyk_do_not_expose") || strings.Contains(string(body), "token") {
		t.Fatalf("credential in live health: %s", body)
	}
	response, err = client.Get(healthURL(record.Port) + "/shutdown")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("shutdown route: %d", response.StatusCode)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("record privacy: %v %v", info, err)
	}
	closeHealth()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record remained: %v", err)
	}
}

func TestDesktopShutdownRequiresPrivateKeyAndVerifiedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.health.json")
	var calls atomic.Int32
	d := &daemon{cfg: Config{Server: "https://lab.example", Profile: "desktop-ada", HealthPath: path}, id: uuid.NewString(), stopControl: func() { calls.Add(1) }}
	closeHealth, err := d.startHealth()
	if err != nil {
		t.Fatal(err)
	}
	defer closeHealth()
	record, err := readHealthRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if record.ShutdownKey == "" || record.ShutdownKey == record.Nonce {
		t.Fatal("missing private shutdown key")
	}
	res, err := http.Get(healthURL(record.Port))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(body), record.ShutdownKey) {
		t.Fatal("health disclosed shutdown key")
	}
	for _, auth := range []string{"", "wrong"} {
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/shutdown", record.Port), nil)
		req.Header.Set("Authorization", auth)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("unauthenticated shutdown: %d", res.StatusCode)
		}
	}
	if err := RequestDesktopShutdown(context.Background(), path, "desktop-other", d.cfg.Server, record.PID); err == nil {
		t.Fatal("wrong profile accepted")
	}
	if err := RequestDesktopShutdown(context.Background(), path, record.Profile, "https://other.example", record.PID); err == nil {
		t.Fatal("wrong server accepted")
	}
	if err := RequestDesktopShutdown(context.Background(), path, record.Profile, record.ServerURL, record.PID+1); err == nil {
		t.Fatal("wrong process accepted")
	}
	for name, mutate := range map[string]func(*HealthRecord){
		"missing key":  func(r *HealthRecord) { r.ShutdownKey = "" },
		"forged key":   func(r *HealthRecord) { r.ShutdownKey = strings.Repeat("a", 64) },
		"reused pid":   func(r *HealthRecord) { r.Started = "another-birth" },
		"wrong daemon": func(r *HealthRecord) { r.DaemonID = uuid.NewString() },
	} {
		t.Run(name, func(t *testing.T) {
			altered := record
			mutate(&altered)
			if err := writeHealthRecord(path, altered); err != nil {
				t.Fatal(err)
			}
			if err := RequestDesktopShutdown(context.Background(), path, record.Profile, record.ServerURL, record.PID); err == nil {
				t.Fatal("accepted invalid shutdown record")
			}
			if err := writeHealthRecord(path, record); err != nil {
				t.Fatal(err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("unverified shutdown invoked")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := RequestDesktopShutdown(context.Background(), path, record.Profile, record.ServerURL, record.PID); err != nil {
				t.Errorf("concurrent shutdown: %v", err)
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatal("verified shutdown not invoked")
	}
}

func TestDesktopShutdownRejectsChallengeReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.health.json")
	var calls atomic.Int32
	d := &daemon{cfg: Config{Server: "https://lab.example", Profile: "desktop-test", HealthPath: path}, id: uuid.NewString(), stopControl: func() { calls.Add(1) }}
	closeHealth, err := d.startHealth()
	if err != nil {
		t.Fatal(err)
	}
	defer closeHealth()
	record, err := readHealthRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	challenge := strings.Repeat("a", 64)
	issue := func() int {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/shutdown-challenge?nonce=%s", record.Port, challenge), nil)
		req.Header.Set("Authorization", "Bearer "+shutdownProof(record.ShutdownKey, "request", challenge, record.Nonce))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	stop := func() int {
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/shutdown", record.Port), nil)
		req.Header.Set("X-Lykeion-Challenge", challenge)
		req.Header.Set("Authorization", "Bearer "+shutdownProof(record.ShutdownKey, "client", challenge, record.Nonce))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if issue() != http.StatusOK {
		t.Fatal("challenge refused")
	}
	if stop() != http.StatusNoContent {
		t.Fatal("valid stop refused")
	}
	if stop() != http.StatusForbidden {
		t.Fatal("replayed stop accepted")
	}
	if issue() != http.StatusForbidden {
		t.Fatal("consumed challenge reissued")
	}
	if calls.Load() != 1 {
		t.Fatal("incorrect shutdown count")
	}
}

func TestHealthCleanupKeepsSuccessorAndRejectsUnsafeRecords(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process identity requires macOS/Linux")
	}
	path := filepath.Join(t.TempDir(), "daemon.health.json")
	d := &daemon{cfg: Config{Server: "https://lab.example", Profile: "desktop", HealthPath: path}, id: uuid.NewString()}
	cleanup, err := d.startHealth()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	record, err := readHealthRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	record.Nonce = "successor"
	if err := atomicJSON(filepath.Dir(path), filepath.Base(path), record); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if got, err := readHealthRecord(path); err != nil || got.Nonce != "successor" {
		t.Fatalf("successor removed: %+v %v", got, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readHealthRecord(path); err == nil {
		t.Fatal("accepted public record")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readHealthRecord(link); err == nil {
		t.Fatal("accepted redirected record")
	}
}

func TestRunPublishesHealthBeforeReadyAndCleansOnReadinessFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process identity requires macOS/Linux")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/workspaces" {
			t.Errorf("unexpected startup request %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	root := t.TempDir()
	path := filepath.Join(root, "daemon.health.json")
	want := errors.New("readiness failed")
	ready, exited := false, false
	cfg := Config{Server: srv.URL, Token: "lyk_private", Device: "Ada", StateDir: filepath.Join(root, "daemon"), Home: t.TempDir(), Profile: "desktop", HealthPath: path, Harnesses: map[string]string{"claude": "/usr/bin/true"}}
	cfg.Ready = func() error {
		ready = true
		live, err := ProbeHealth(t.Context(), path, cfg.Profile, cfg.Server)
		if err != nil {
			t.Fatal(err)
		}
		if live.WorkspaceCount != 0 || live.DeviceName != "Ada" {
			t.Fatalf("startup diagnostics %+v", live)
		}
		return want
	}
	cfg.OnExit = func() {
		exited = true
		lock, err := ownState(cfg.StateDir)
		if err == nil {
			lock.Close()
			t.Error("exit callback ran outside ownership")
		}
	}
	if err := Run(t.Context(), cfg); !errors.Is(err, want) {
		t.Fatalf("readiness error: %v", err)
	}
	if !ready || !exited {
		t.Fatalf("callbacks lost: ready=%v exited=%v", ready, exited)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("health survived failed startup: %v", err)
	}
}

func TestHealthWorkspaceCountTracksCurrentMembershipsWithoutRuntimes(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process identity requires macOS/Linux")
	}
	first, second := uuid.New(), uuid.New()
	var memberships atomic.Value
	memberships.Store([]uuid.UUID{first, second})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/daemon/workspaces" {
			rows := []map[string]uuid.UUID{}
			for _, id := range memberships.Load().([]uuid.UUID) {
				rows = append(rows, map[string]uuid.UUID{"id": id})
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "daemon.health.json")
	d := &daemon{cfg: Config{Server: srv.URL, Profile: "desktop", HealthPath: path}, id: uuid.NewString(), api: &apiclient.Client{Server: srv.URL}, knownWorkspaces: map[uuid.UUID]bool{}}
	if err := d.register(t.Context(), d.id); err != nil {
		t.Fatal(err)
	}
	cleanup, err := d.startHealth()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, ids := range [][]uuid.UUID{{first, second}, {second}, {}} {
		memberships.Store(ids)
		if err := d.refreshReferences(t.Context()); err != nil {
			t.Fatal(err)
		}
		live, err := ProbeHealth(t.Context(), path, d.cfg.Profile, d.cfg.Server)
		if err != nil {
			t.Fatal(err)
		}
		if live.WorkspaceCount != len(ids) {
			t.Fatalf("historical memberships counted: want %d, got %+v", len(ids), live)
		}
	}
	if len(d.knownWorkspaces) != 2 {
		t.Fatal("historical registration guard changed")
	}
}

func TestHealthPublicationDoesNotChangeConfigParentPermissions(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process identity requires macOS/Linux")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	d := &daemon{cfg: Config{Server: "https://lab.example", HealthPath: filepath.Join(dir, "daemon.health.json")}, id: uuid.NewString()}
	cleanup, err := d.startHealth()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("config parent permissions changed to %o", info.Mode().Perm())
	}
	info, err = os.Stat(d.cfg.HealthPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("health record is not private: %o", info.Mode().Perm())
	}
}
