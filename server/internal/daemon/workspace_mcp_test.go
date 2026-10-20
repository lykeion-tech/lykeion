package daemon

import (
	"bytes"
	"context"
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
	"lykeion/server/internal/availability"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/internal/toolroute"
	"lykeion/server/internal/workspacemcp"
	"lykeion/server/pkg/agent"
)

func TestWorkspaceCommandUsesClaimedDaemon(t *testing.T) {
	f := fixture(t)
	ctx := t.Context()
	keys := workspaceTestKeys()
	f.runs.Keys = keys
	saved := workspaceTestCommand(t, f, keys)
	rts, err := f.runs.Register(ctx, f.user, f.ws, "workspace-command", "laptop", []string{"claude"}, nil, agent.Platform{})
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
	if _, err := os.Stat(filepath.Join(d.connectorDir, "stub.pid")); err != nil {
		t.Fatal("command did not start in the door's folder", err)
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
					w.Header().Set(workspacemcp.RecordHeader, "fixture-receipt")
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
	rts, err := f.runs.Register(ctx, f.user, f.ws, "replace", "laptop", []string{"claude"}, nil, agent.Platform{})
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
	pids, err := os.ReadFile(filepath.Join(d.connectorDir, "stub.pid"))
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
		if _, err := os.Stat(filepath.Join(d.connectorDir, "stub.started")); err == nil {
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
	must(t, os.WriteFile(filepath.Join(d.connectorDir, "stub.release"), []byte("release"), 0600))
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
	rts, err := f.runs.Register(ctx, f.user, f.ws, "remote", "laptop", []string{"claude"}, nil, agent.Platform{})
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

func TestWorkspaceCommandStartupCancellation(t *testing.T) {
	for _, phase := range []string{"preflight", "initialize"} {
		t.Run(phase, func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "preflight" {
					started <- struct{}{}
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return
				}
				w.Write([]byte("[]"))
			}))
			defer srv.Close()
			defer close(release)
			dir := t.TempDir()
			self, err := os.Executable()
			must(t, err)
			d := &Door{Server: srv.URL, CallToken: "ltk_private", WorkspaceConnectors: []service.WorkspaceConnectorClaim{{ID: uuid.New(), Name: "blocked", Transport: "command", Command: []string{self}, Env: map[string]string{"LYK_DOOR_STUB": "1", "LYK_DOOR_STUB_BLOCK_INIT": "1"}}}, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: dir} }}
			must(t, d.Start())
			defer d.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, _, err := d.callTool(ctx, nil, callIn{Server: "blocked", Tool: "echo"}); done <- err }()
			if phase == "preflight" {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("preflight not reached")
				}
			} else {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(d.connectorDir, "stub.pid")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("initialization not reached")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled startup answered")
				}
			case <-time.After(time.Second):
				t.Fatal("request cancellation blocked behind startup")
			}
			if phase == "initialize" {
				raw, err := os.ReadFile(filepath.Join(d.connectorDir, "stub.pid"))
				must(t, err)
				for _, v := range strings.Fields(string(raw)) {
					pid, _ := strconv.Atoi(v)
					for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil; {
						time.Sleep(10 * time.Millisecond)
					}
					if syscall.Kill(pid, 0) == nil {
						t.Fatalf("partial process %d survived", pid)
					}
				}
			}
		})
	}
}

func TestWorkspaceMCPRecordRejectsChangedAuthority(t *testing.T) {
	for _, transport := range []string{"url", "command"} {
		for _, mutation := range []string{"replace", "delete", "permission"} {
			t.Run(transport+"/"+mutation, func(t *testing.T) {
				f := fixture(t)
				ctx := t.Context()
				svc := workspacemcp.Service{Store: f.runs.Store, Secrets: f.runs.Keys}
				upstream := httptest.NewTLSServer(labMCP(nil))
				defer upstream.Close()
				f.runs.Keys.HTTP = upstream.Client()
				var id uuid.UUID
				name, tool := "remote", "lookup"
				if transport == "command" {
					id = workspaceTestCommand(t, f, f.runs.Keys)
					name, tool = "workspace-command", "echo"
				} else {
					saved, err := svc.Create(ctx, f.ws, f.user, name, workspacemcp.Config{URL: upstream.URL})
					must(t, err)
					id = saved.ID
				}
				ids := []uuid.UUID{}
				for i := 0; i < 2; i++ {
					refs := map[string][]map[string]any{}
					if i == 0 {
						refs["member-1"] = []map[string]any{{"name": name, "workspace": id.String()}}
					}
					parsed, err := pack.Parse(testutil.Pack(t, testutil.PackOpts{Name: fmt.Sprintf("team-%d", i), Connectors: refs}))
					must(t, err)
					must(t, f.runs.Store.InWorkspace(ctx, f.ws, f.user, func(tx *store.Tx) error {
						p, e := pack.Insert(ctx, tx, "fixture", parsed, nil)
						ids = append(ids, p.ID)
						return e
					}))
				}
				_, err := f.env.Owner.Exec(ctx, `UPDATE agent_run SET role='member-1' WHERE id=$1`, f.id)
				must(t, err)
				_, err = f.env.Owner.Exec(ctx, `INSERT INTO card_team(workspace_id,card_id,board_column,pack_id) VALUES($1,$2,'grounding',$3)`, f.ws, f.card, ids[0])
				must(t, err)
				var agentID uuid.UUID
				must(t, f.env.Owner.QueryRow(ctx, `INSERT INTO agent(workspace_id,name,harness,owner_id) VALUES($1,'member','claude',$2) RETURNING id`, f.ws, f.user).Scan(&agentID))
				_, err = f.env.Owner.Exec(ctx, `INSERT INTO team_agent(workspace_id,pack_id,user_id,member,agent_id) VALUES($1,$2,$3,'member-1',$4)`, f.ws, ids[0], f.user, agentID)
				must(t, err)
				rts, err := f.runs.Register(ctx, f.user, f.ws, "record-race", "laptop", []string{"claude"}, nil, agent.Platform{})
				must(t, err)
				ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rts[0].ID}
				claims, err := f.runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 1)
				must(t, err)
				if len(claims) != 1 {
					t.Fatal("no claim")
				}
				ref.Attempt = claims[0].AgentRun.Attempt
				must(t, f.runs.Start(ctx, f.user, ref, claims[0].AgentRun.ID))
				var intercepted atomic.Bool
				base, _ := url.Parse(f.cfg.Server)
				proxy := httputil.NewSingleHostReverseProxy(base)
				bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/role/calls" {
						intercepted.Store(true)
						var err error
						switch mutation {
						case "delete":
							err = svc.Delete(ctx, f.ws, f.user, id, 1)
						case "replace":
							config := workspacemcp.Config{URL: upstream.URL}
							if transport == "command" {
								config = workspacemcp.Config{Command: "replacement"}
							}
							_, err = svc.Update(ctx, f.ws, f.user, id, 1, name, config)
						case "permission":
							_, err = f.env.Owner.Exec(ctx, `UPDATE card_team SET pack_id=$1 WHERE workspace_id=$2 AND card_id=$3`, ids[1], f.ws, f.card)
						}
						if err != nil {
							t.Error(err)
							http.Error(w, "mutation failed", 500)
							return
						}
					}
					proxy.ServeHTTP(w, r)
				}))
				defer bridge.Close()
				dir := t.TempDir()
				d := &Door{Server: bridge.URL, CallToken: claims[0].CallToken, WorkspaceConnectors: claims[0].WorkspaceConnectors, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: dir} }}
				must(t, d.Start())
				defer d.Close()
				answer, err := callTool(t, d, name, tool, map[string]any{"q": "stale"})
				if !intercepted.Load() {
					t.Fatal("did not reach execution-to-record boundary")
				}
				if err == nil || answer != "" {
					t.Fatalf("stale answer escaped: %q %v", answer, err)
				}
				var records, events int
				must(t, f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='call'`, f.card).Scan(&records))
				must(t, f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='record.created' AND payload->>'kind'='call'`, f.ws).Scan(&events))
				if records != 0 || events != 0 {
					t.Fatalf("stale evidence persisted records=%d events=%d", records, events)
				}
			})
		}
	}
}

func TestWorkspaceCommandDisableKeepsStartedAnswer(t *testing.T) {
	f := fixture(t)
	ctx := t.Context()
	id := workspaceTestCommand(t, f, f.runs.Keys)
	rts, err := f.runs.Register(ctx, f.user, f.ws, "policy-command", "laptop", []string{"claude"}, nil, agent.Platform{})
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
	defer d.Close()
	type result struct {
		answer string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		a, e := callTool(t, d, "workspace-command", "echo", map[string]any{"q": "workspace-wait"})
		done <- result{a, e}
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(filepath.Join(d.connectorDir, "stub.started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not enter execution")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err = availability.Set(ctx, f.runs.Store, f.ws, f.user, "mcp", id.String(), 0, false)
	must(t, err)
	answer, denied := callTool(t, d, "workspace-command", "echo", map[string]any{"q": "concurrent-after-disable"})
	if denied == nil || answer != "" {
		t.Fatalf("disabled concurrent call: %q %v", answer, denied)
	}
	must(t, os.WriteFile(filepath.Join(d.connectorDir, "stub.release"), []byte("release"), 0600))
	select {
	case res := <-done:
		if res.err != nil || !strings.Contains(res.answer, "echo workspace-wait") {
			t.Fatalf("in-flight completion: %q %v", res.answer, res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight completion hung")
	}
	var records int
	must(t, f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='call'`, f.card).Scan(&records))
	if records != 1 {
		t.Fatalf("completed records: %d", records)
	}
	answer, err = callTool(t, d, "workspace-command", "echo", map[string]any{"q": "after"})
	if err == nil || answer != "" {
		t.Fatalf("disabled new call: %q %v", answer, err)
	}
	must(t, f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='call'`, f.card).Scan(&records))
	if records != 1 {
		t.Fatalf("new disabled record: %d", records)
	}
}

func TestDoorRoutingSkipsDisabledWorkspaceClaims(t *testing.T) {
	for _, transport := range []string{"command", "url"} {
		t.Run(transport, func(t *testing.T) {
			f := fixture(t)
			ctx := t.Context()
			j := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in struct{ Questions map[string]json.RawMessage }
				json.NewDecoder(r.Body).Decode(&in)
				answers := map[string]any{}
				for id := range in.Questions {
					answers[id] = map[string]any{"type": "noul", "noul": .9}
				}
				json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": answers})
			}))
			defer j.Close()
			f.h.Engine = &flow.Engine{Store: f.runs.Store, Blobs: f.h.Blobs, Jev: &jev.Client{BaseURL: j.URL, Key: "fixture", Model: jev.Model}}
			upstream := httptest.NewTLSServer(labMCP(nil))
			defer upstream.Close()
			f.runs.Keys.HTTP = upstream.Client()
			svc := workspacemcp.Service{Store: f.runs.Store, Secrets: f.runs.Keys}
			kept, err := svc.Create(ctx, f.ws, f.user, "kept", workspacemcp.Config{URL: upstream.URL})
			must(t, err)
			var disabled uuid.UUID
			if transport == "command" {
				disabled = workspaceTestCommand(t, f, f.runs.Keys)
			} else {
				c, e := svc.Create(ctx, f.ws, f.user, "disabled", workspacemcp.Config{URL: upstream.URL})
				must(t, e)
				disabled = c.ID
			}
			rts, err := f.runs.Register(ctx, f.user, f.ws, "discovery-policy", "laptop", []string{"claude"}, nil, agent.Platform{})
			must(t, err)
			ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rts[0].ID}
			claims, err := f.runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 1)
			must(t, err)
			if len(claims) != 1 || len(claims[0].WorkspaceConnectors) != 2 {
				t.Fatalf("claim: %+v", claims)
			}
			ref.Attempt = claims[0].AgentRun.Attempt
			must(t, f.runs.Start(ctx, f.user, ref, claims[0].AgentRun.ID))
			_, err = availability.Set(ctx, f.runs.Store, f.ws, f.user, "mcp", disabled.String(), 0, false)
			must(t, err)
			d := &Door{Server: f.cfg.Server, RunToken: claims[0].Token, CallToken: claims[0].CallToken, WorkspaceConnectors: claims[0].WorkspaceConnectors, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: t.TempDir()} }}
			must(t, d.Start())
			req := httptest.NewRequest("POST", "/tools", strings.NewReader(`{"need":"look up a gene"}`))
			response := httptest.NewRecorder()
			d.tools(response, req)
			defer d.Close()
			if response.Code != 200 {
				t.Fatalf("disabled claim blocked routing: %d %s", response.Code, response.Body.String())
			}
			var result toolroute.Result
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, tool := range result.Tools {
				if tool.Server == kept.Name {
					found = true
				}
				if tool.Server == "disabled" || tool.Server == "workspace-command" {
					t.Fatal("disabled tool routed")
				}
			}
			if !found {
				t.Fatalf("enabled connector omitted: %+v", result.Tools)
			}
		})
	}
}
