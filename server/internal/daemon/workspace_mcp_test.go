package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/service"
	"lykeion/server/internal/workspacemcp"
	"lykeion/server/pkg/agent"
)

func TestWorkspaceCommandUsesClaimedDaemon(t *testing.T) {
	f := fixture(t)
	ctx := t.Context()
	keys := workspaceTestKeys()
	f.runs.Keys = keys
	saved := workspaceTestCommand(t, f, keys)
	rts, err := f.runs.Register(ctx, f.user, f.ws, "workspace-command", "laptop", []string{"claude"}, nil)
	must(t, err)
	claims, err := f.runs.Claim(ctx, f.user, []service.RuntimeRef{{WorkspaceID: f.ws, RuntimeID: rts[0].ID}}, 1)
	must(t, err)
	if len(claims) != 1 || len(claims[0].WorkspaceConnectors) != 1 || claims[0].WorkspaceConnectors[0].ID != saved {
		t.Fatalf("claim: %+v", claims)
	}
	must(t, f.runs.Start(ctx, f.user, service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rts[0].ID, Attempt: claims[0].AgentRun.Attempt}, claims[0].AgentRun.ID))
	dir := t.TempDir()
	d := &Door{Server: f.cfg.Server, RunToken: claims[0].Token, CallToken: claims[0].CallToken, WorkspaceConnectors: claims[0].WorkspaceConnectors, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: dir} }}
	must(t, d.Start())
	t.Cleanup(d.Close)
	answer, err := callTool(t, d, "workspace-command", "echo", map[string]any{"q": "workspace"})
	must(t, err)
	if !strings.Contains(answer, "echo workspace") {
		t.Fatal(answer)
	}
	if _, err := os.Stat(filepath.Join(dir, "stub.pid")); err != nil {
		t.Fatal("command did not use claimed folder", err)
	}
}

func TestWorkspaceMCPCallIsRecordedOnce(t *testing.T) {
	id := uuid.New()
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "record", true: "withhold"}[reject], func(t *testing.T) {
			records := 0
			brokerCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer ltk_private" {
					t.Error("private call token missing")
				}
				switch r.URL.Path {
				case "/api/role/mcp-servers/" + id.String() + "/call":
					brokerCalls++
					json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "server answer"}}})
				case "/api/role/calls":
					records++
					if reject {
						http.Error(w, "offline", 503)
						return
					}
					json.NewEncoder(w).Encode(map[string]string{"key": "c17"})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			d := &Door{Server: srv.URL, CallToken: "ltk_private", WorkspaceConnectors: []service.WorkspaceConnectorClaim{{ID: id, Name: "remote", Transport: "url"}}, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: t.TempDir()} }}
			must(t, d.Start())
			defer d.Close()
			answer, err := callTool(t, d, "remote", "echo", nil)
			if reject {
				if err == nil || answer != "" || !strings.Contains(err.Error(), "withheld") {
					t.Fatalf("answer leaked: %q %v", answer, err)
				}
			} else if err != nil || answer != "call:c17\nserver answer" {
				t.Fatalf("answer: %q %v", answer, err)
			}
			if records != 1 || brokerCalls != 1 {
				t.Fatalf("records=%d calls=%d", records, brokerCalls)
			}
		})
	}
}

func workspaceTestKeys() *connectors.Client {
	return &connectors.Client{Secret: bytes.Repeat([]byte{7}, 32)}
}
func workspaceTestCommand(t *testing.T, f *daemonFixture, keys *connectors.Client) uuid.UUID {
	t.Helper()
	self, err := os.Executable()
	must(t, err)
	// A basename satisfies the same Team checker; PATH locates the real fixture.
	t.Setenv("PATH", filepath.Dir(self)+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := workspacemcp.Service{Store: f.runs.Store, Secrets: keys}
	saved, err := s.Create(t.Context(), f.ws, f.user, "workspace-command", workspacemcp.Config{Command: filepath.Base(self), Env: map[string]string{"LYK_DOOR_STUB": "1"}})
	must(t, err)
	return saved.ID
}

func TestWorkspaceCommandReplacementWithholdsAndClosesOldProcess(t *testing.T) {
	f := fixture(t)
	keys := f.runs.Keys
	id := workspaceTestCommand(t, f, keys)
	ctx := t.Context()
	rts, err := f.runs.Register(ctx, f.user, f.ws, "replace", "laptop", []string{"claude"}, nil)
	must(t, err)
	ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rts[0].ID}
	claims, err := f.runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 1)
	must(t, err)
	if len(claims) != 1 {
		t.Fatal("no claim")
	}
	ref.Attempt = claims[0].AgentRun.Attempt
	must(t, f.runs.Start(ctx, f.user, ref, claims[0].AgentRun.ID))
	dir := t.TempDir()
	d := &Door{Server: f.cfg.Server, CallToken: claims[0].CallToken, WorkspaceConnectors: claims[0].WorkspaceConnectors, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: dir} }}
	must(t, d.Start())
	t.Cleanup(d.Close)
	_, err = callTool(t, d, "workspace-command", "echo", map[string]any{"q": "before"})
	must(t, err)
	pids, err := os.ReadFile(filepath.Join(dir, "stub.pid"))
	must(t, err)
	pending := make(chan error, 1)
	go func() {
		answer, err := callTool(t, d, "workspace-command", "echo", map[string]any{"q": "workspace-wait"})
		if answer != "" {
			pending <- fmt.Errorf("answer leaked: %s", answer)
			return
		}
		pending <- err
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(filepath.Join(dir, "stub.started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	svc := workspacemcp.Service{Store: f.runs.Store, Secrets: keys}
	_, err = svc.Update(ctx, f.ws, f.user, id, 1, "workspace-command", workspacemcp.Config{Command: "replacement"})
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "stub.release"), []byte("release"), 0600))
	select {
	case err := <-pending:
		if err == nil || !strings.Contains(err.Error(), "withheld") {
			t.Fatalf("mid-call replacement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command replacement hung")
	}
	var records int
	must(t, f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='call'`, f.card).Scan(&records))
	if records != 1 {
		t.Fatalf("stale command answer recorded: %d", records)
	}
	answer, err := callTool(t, d, "workspace-command", "echo", map[string]any{"q": "after"})
	if err == nil || answer != "" {
		t.Fatalf("old command executed after replacement: %q %v", answer, err)
	}
	for _, pid := range strings.Fields(string(pids)) {
		n, _ := strconv.Atoi(pid)
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && syscall.Kill(n, 0) == nil; {
			time.Sleep(10 * time.Millisecond)
		}
		if syscall.Kill(n, 0) == nil {
			t.Fatalf("old process %d survived replacement", n)
		}
	}
}

func TestWorkspaceRemoteBrokerAndDoorRecordRealAnswerOnce(t *testing.T) {
	f := fixture(t)
	ctx := t.Context()
	remoteFixture := labMCP(nil)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Error("upstream header missing")
			w.WriteHeader(401)
			return
		}
		// The same fixture instance keeps each SDK connection's state.
		remoteFixture.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	f.runs.Keys.HTTP = upstream.Client()
	svc := workspacemcp.Service{Store: f.runs.Store, Secrets: f.runs.Keys}
	saved, err := svc.Create(ctx, f.ws, f.user, "remote", workspacemcp.Config{URL: upstream.URL, Headers: map[string]string{"Authorization": "Bearer upstream-secret"}})
	must(t, err)
	rts, err := f.runs.Register(ctx, f.user, f.ws, "remote", "laptop", []string{"claude"}, nil)
	must(t, err)
	ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rts[0].ID}
	claims, err := f.runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 1)
	must(t, err)
	if len(claims) != 1 {
		t.Fatal("no claim")
	}
	raw, _ := json.Marshal(claims[0])
	if strings.Contains(string(raw), "upstream-secret") || strings.Contains(string(raw), upstream.URL) {
		t.Fatal("remote config left server")
	}
	if len(claims[0].WorkspaceConnectors) != 1 || claims[0].WorkspaceConnectors[0].ID != saved.ID {
		t.Fatal("remote missing from claim")
	}
	ref.Attempt = claims[0].AgentRun.Attempt
	must(t, f.runs.Start(ctx, f.user, ref, claims[0].AgentRun.ID))
	var reject atomic.Bool
	base, _ := url.Parse(f.cfg.Server)
	proxy := httputil.NewSingleHostReverseProxy(base)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/role/calls" && reject.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer bridge.Close()
	d := &Door{Server: bridge.URL, CallToken: claims[0].CallToken, WorkspaceConnectors: claims[0].WorkspaceConnectors, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: t.TempDir()} }}
	must(t, d.Start())
	defer d.Close()
	answer, err := callTool(t, d, "remote", "lookup", map[string]any{"q": "TP53"})
	must(t, err)
	if !strings.HasPrefix(answer, "call:c1\n") || !strings.Contains(answer, "TP53") {
		t.Fatal(answer)
	}
	var count int
	must(t, f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='call'`, f.card).Scan(&count))
	if count != 1 {
		t.Fatalf("records=%d", count)
	}
	reject.Store(true)
	answer, err = callTool(t, d, "remote", "lookup", map[string]any{"q": "withheld"})
	if err == nil || answer != "" || !strings.Contains(err.Error(), "withheld") {
		t.Fatalf("answer not withheld: %q %v", answer, err)
	}
	must(t, f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='call'`, f.card).Scan(&count))
	if count != 1 {
		t.Fatalf("duplicate records=%d", count)
	}
}
