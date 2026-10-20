package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
)

// A test binary started with LYK_DOOR_STUB=1 is a stdio MCP server: the
// stand-in for a Team's local command.
func TestMain(m *testing.M) {
	if os.Getenv("LYK_DOOR_STUB") == "1" {
		stubCommand()
		os.Exit(0)
	}
	if os.Getenv("LYK_KERNEL_STUB") == "1" {
		stubBridge()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// stubCommand notes its pid, a child's pid and whether it saw a run token,
// in its working folder, then answers echo over stdin and stdout.
func stubCommand() {
	child := exec.Command("sleep", "60")
	if child.Start() != nil {
		os.Exit(3)
	}
	os.WriteFile("stub.pid", []byte(strconv.Itoa(os.Getpid())+" "+strconv.Itoa(child.Process.Pid)), 0600)
	os.WriteFile("stub.env", []byte("LYK_TOKEN="+os.Getenv("LYK_TOKEN")+" LYK_DOOR_TOKEN="+os.Getenv("LYK_DOOR_TOKEN")), 0600)
	if os.Getenv("LYK_DOOR_STUB_BLOCK_INIT") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "stub", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo"}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Q string `json:"q"`
	}) (*mcp.CallToolResult, any, error) {
		if in.Q == "workspace-wait" {
			os.WriteFile("stub.started", []byte("ready"), 0600)
			for {
				if _, err := os.Stat("stub.release"); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo " + in.Q}}}, nil, nil
	})
	srv.Run(context.Background(), &mcp.StdioTransport{})
}

// lykeion stands in for the server: it answers /api/role/calls with c1, c2, …
// and /api/role/runs with x1, x2, …
// and serves a catalogue fetch on /mcp/sources whose call key is c7, beside
// the sources tools.
type lykeion struct {
	*httptest.Server
	mu         sync.Mutex
	posts      []map[string]any
	fetches    []map[string]any
	postAuth   []string // on /api/role/calls
	sourceAuth []string // on /mcp/sources
	runs       []map[string]any
	runSizes   []int // the length of every body /api/role/runs received
	failRuns   bool  // answer /api/role/runs with 500
	noCard     bool  // answer /api/role/runs as for a run without a card
}

func newLykeion(t *testing.T) *lykeion {
	l := &lykeion{}
	src := mcp.NewServer(&mcp.Implementation{Name: "lyk_sources", Version: "0"}, nil)
	mcp.AddTool(src, &mcp.Tool{Name: "fetch"}, func(_ context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		l.mu.Lock()
		l.fetches = append(l.fetches, in)
		l.sourceAuth = append(l.sourceAuth, req.Extra.Header.Get("Authorization"))
		l.mu.Unlock()
		return nil, map[string]any{"call": "c7", "connector": in["connector"], "status": 200, "body": "P01308 insulin"}, nil
	})
	for _, name := range []string{"search_literature", "list_connectors"} {
		mcp.AddTool(src, &mcp.Tool{Name: name}, func(_ context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			l.mu.Lock()
			l.sourceAuth = append(l.sourceAuth, req.Extra.Header.Get("Authorization"))
			l.mu.Unlock()
			return nil, map[string]any{"results": []any{map[string]any{"id": "openalex:W1", "title": name + " " + fmt.Sprint(in["query"])}}}, nil
		})
	}
	sources := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return src }, &mcp.StreamableHTTPOptions{Stateless: true})
	l.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp/sources":
			sources.ServeHTTP(w, r)
		case "/api/role/calls":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			l.mu.Lock()
			l.posts = append(l.posts, body)
			l.postAuth = append(l.postAuth, r.Header.Get("Authorization"))
			n := len(l.posts)
			l.mu.Unlock()
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"key": "c" + strconv.Itoa(n)})
		case "/api/role/runs":
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(b, &body)
			l.mu.Lock()
			defer l.mu.Unlock()
			l.runSizes = append(l.runSizes, len(b))
			if l.failRuns || r.Header.Get("Authorization") != "Bearer ltk_call" {
				w.WriteHeader(500)
				return
			}
			l.runs = append(l.runs, body)
			if l.noCard {
				w.WriteHeader(201)
				w.Write([]byte("{}"))
				return
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"key": "x" + strconv.Itoa(len(l.runs))})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(l.Close)
	return l
}

// labMCP is a lab's MCP server: lookup answers, slow sleeps two seconds,
// big answers N bytes.
func labMCP(hits *atomic.Int32) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "lab", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "lookup"}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Q string `json:"q"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "gene " + in.Q}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "slow"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "late"}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "big"}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		N int `json:"n"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("a", in.N)}}}, nil, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		h.ServeHTTP(w, r)
	})
}

// labServer is an add-on server on plain http and loopback, as add-ons may be.
func labServer(t *testing.T) string {
	s := httptest.NewServer(labMCP(nil))
	t.Cleanup(s.Close)
	return s.URL
}

func openDoor(t *testing.T, l *lykeion, dir string, conns []pack.Connector, addons ...agent.MCPServer) *Door {
	t.Helper()
	d := &Door{Server: l.URL, Sources: l.URL + "/mcp/sources", RunToken: "ltk_run", CallToken: "ltk_call", Connectors: conns, Addons: addons,
		Child: func() agent.ExecOptions {
			return agent.ExecOptions{Dir: dir, MCPToken: "ltk_run", Env: []string{"LYK_TOKEN=ltk_run", "LYK_DOOR_TOKEN=secret"}}
		}}
	must(t, d.Start())
	t.Cleanup(func() { d.Close() })
	return d
}

// callTool calls the door's call_tool as the CLI would and answers its text.
func callTool(t *testing.T, d *Door, server, tool string, args map[string]any) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := mcp.NewClient(&mcp.Implementation{Name: "cli", Version: "0"}, nil)
	s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: d.URL, HTTPClient: &http.Client{Transport: bearer(d.Token)}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return "", err
	}
	defer s.Close()
	call := map[string]any{"server": server, "tool": tool}
	if args != nil {
		call["args"] = args
	}
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "call_tool", Arguments: call})
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return "", errors.New(text.String())
	}
	return text.String(), nil
}

func TestCallToolRecordsEveryAnswer(t *testing.T) {
	l := newLykeion(t)
	d := openDoor(t, l, t.TempDir(), nil, agent.MCPServer{Name: "lab", URL: labServer(t)})
	for i, q := range []string{"TP53", "BRCA1"} {
		got, err := callTool(t, d, "lab", "lookup", map[string]any{"q": q})
		if err != nil {
			t.Fatal(err)
		}
		want := "call:c" + strconv.Itoa(i+1) + "\n"
		if !strings.HasPrefix(got, want) || !strings.Contains(got, "gene "+q) {
			t.Fatalf("answer %d: %q", i+1, got)
		}
	}
	if len(l.posts) != 2 || len(l.fetches) != 0 {
		t.Fatalf("%d call posts, %d fetches", len(l.posts), len(l.fetches))
	}
	p := l.posts[0]
	if p["server"] != "lab" || p["tool"] != "lookup" || p["answer"] != "gene TP53" || p["args"].(map[string]any)["q"] != "TP53" {
		t.Fatalf("posted %v", p)
	}
	// calls are recorded with the door's own token, which the agent never holds
	for _, a := range l.postAuth {
		if a != "Bearer ltk_call" {
			t.Fatalf("posted with %q, not the door's call token", a)
		}
	}
}

func TestCatalogueCallsGoThroughTheServer(t *testing.T) {
	l := newLykeion(t)
	d := openDoor(t, l, t.TempDir(), []pack.Connector{{Catalogue: "uniprot"}})
	got, err := callTool(t, d, "uniprot", "fetch", map[string]any{"path": "/uniprotkb/P01308.txt", "connector": "ensembl"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "call:c7\n") || !strings.Contains(got, "P01308 insulin") {
		t.Fatalf("answer %q", got)
	}
	// the server's fetch writes the record; the door posts none of its own
	if len(l.fetches) != 1 || len(l.posts) != 0 || l.sourceAuth[0] != "Bearer ltk_run" {
		t.Fatalf("%d fetches, %d posts, auth %v", len(l.fetches), len(l.posts), l.sourceAuth)
	}
	// the connector is the one the claim names, whatever the args say
	if f := l.fetches[0]; f["connector"] != "uniprot" || f["path"] != "/uniprotkb/P01308.txt" {
		t.Fatalf("fetched %v", f)
	}
}

func TestALocalConnectorRunsOnlyForWhoAddedTheTeam(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()
	blobs := storage.Local{Dir: t.TempDir()}
	f.runs.Blobs = blobs
	local := map[string]any{"name": "stub", "command": []string{"stub-mcp"}, "env": map[string]string{"STUB_MODE": "1"}}
	zipBytes := testutil.Pack(t, testutil.PackOpts{Catalogue: map[string][]string{"member-1": {"uniprot"}}, Connectors: map[string][]map[string]any{"member-1": {local}}})
	p, err := pack.Parse(zipBytes)
	must(t, err)
	key, err := pack.PutBlob(ctx, blobs, f.ws, zipBytes, p.SHA256)
	must(t, err)
	var packID uuid.UUID
	must(t, f.runs.Store.InWorkspace(ctx, f.ws, f.user, func(tx *store.Tx) error {
		row, err := pack.Insert(ctx, tx, key, p, nil)
		packID = row.ID
		return err
	}))
	_, err = f.env.Owner.Exec(ctx, `INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, f.ws, f.card, packID)
	must(t, err)
	rts, err := f.runs.Register(ctx, f.user, f.ws, "door-test", "laptop", []string{"claude"}, nil, agent.Platform{})
	must(t, err)
	ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rts[0].ID}
	_, err = f.env.Owner.Exec(ctx, `UPDATE runtime SET max_concurrent = 32 WHERE id = $1`, rts[0].ID)
	must(t, err)
	claim := func() []pack.Connector {
		t.Helper()
		var id uuid.UUID
		must(t, f.runs.Store.InWorkspace(ctx, f.ws, f.user, func(tx *store.Tx) error {
			r, err := f.runs.Enqueue(ctx, tx, f.user, service.NewAgentRun{Card: f.card, Column: "grounding", Round: 1, Role: "member-1", Harness: "claude", Prompt: "p", MaxAttempts: 1})
			id = r.ID
			return err
		}))
		cs, err := f.runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 10)
		must(t, err)
		for _, c := range cs {
			if c.AgentRun.ID == id {
				return c.Connectors
			}
		}
		t.Fatal("the run was not claimed")
		return nil
	}
	names := func(cs []pack.Connector) (out []string) {
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return out
	}

	// the run's researcher never added this Team version: no local command
	got := claim()
	if !slices.Equal(names(got), []string{"uniprot"}) {
		t.Fatalf("claim for a researcher who never added the Team: %+v", got)
	}
	// and the door refuses a server the claim does not name, starting nothing
	dir := t.TempDir()
	d := openDoor(t, newLykeion(t), dir, got)
	if _, err := callTool(t, d, "stub", "echo", map[string]any{"q": "x"}); err == nil || !strings.Contains(err.Error(), "stub") {
		t.Fatalf("an undeclared server answered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stub.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a command started: %v", err)
	}

	// once the researcher has added the Team version, the command is theirs to run
	var agentID uuid.UUID
	must(t, f.env.Owner.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, 'member-1 · demo', 'claude', $2) RETURNING id`, f.ws, f.user).Scan(&agentID))
	_, err = f.env.Owner.Exec(ctx, `INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'member-1', $4)`, f.ws, packID, f.user, agentID)
	must(t, err)
	got = claim()
	if !slices.Equal(names(got), []string{"uniprot", "stub"}) || !slices.Equal(got[1].Command, []string{"stub-mcp"}) || got[1].Env["STUB_MODE"] != "1" {
		t.Fatalf("claim for the researcher who added the Team: %+v", got)
	}
}

func TestTheDoorRefusesWithoutItsToken(t *testing.T) {
	d := openDoor(t, newLykeion(t), t.TempDir(), nil)
	if !strings.HasPrefix(d.URL, "http://127.0.0.1:") || d.Token == "" || strings.HasPrefix(d.Token, "lyk_") || strings.HasPrefix(d.Token, "ltk_") {
		t.Fatalf("door %s token %q", d.URL, d.Token)
	}
	base := strings.TrimSuffix(d.URL, "/mcp")
	for _, path := range []string{"/mcp", "/tools"} {
		for _, auth := range []string{"", "Bearer ltk_run", "Bearer " + d.Token + "x"} {
			req, _ := http.NewRequest("POST", base+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			res, err := http.DefaultClient.Do(req)
			must(t, err)
			res.Body.Close()
			if res.StatusCode != 401 {
				t.Fatalf("%s with %q: %d", path, auth, res.StatusCode)
			}
		}
	}
	if _, err := callTool(t, d, "none", "x", nil); err == nil {
		t.Fatal("an unknown server answered")
	}
}

func TestALocalConnectorStopsWithTheRun(t *testing.T) {
	self, err := os.Executable()
	must(t, err)
	dir := t.TempDir()
	d := openDoor(t, newLykeion(t), dir, []pack.Connector{{Name: "stub", Command: []string{self}, Env: map[string]string{"LYK_DOOR_STUB": "1"}}})
	got, err := callTool(t, d, "stub", "echo", map[string]any{"q": "hi"})
	if err != nil || got != "call:c1\necho hi" {
		t.Fatalf("%q %v", got, err)
	}
	// started in the run's folder, without the run's credentials
	pids, err := os.ReadFile(filepath.Join(dir, "stub.pid"))
	must(t, err)
	if env, _ := os.ReadFile(filepath.Join(dir, "stub.env")); string(env) != "LYK_TOKEN= LYK_DOOR_TOKEN=" {
		t.Fatalf("the command saw %q", env)
	}
	// a second call reuses the running command
	if got, err := callTool(t, d, "stub", "echo", map[string]any{"q": "again"}); err != nil || got != "call:c2\necho again" {
		t.Fatalf("%q %v", got, err)
	}
	if again, _ := os.ReadFile(filepath.Join(dir, "stub.pid")); string(again) != string(pids) {
		t.Fatal("the command was started twice")
	}
	d.Close()
	for _, field := range strings.Fields(string(pids)) {
		pid, _ := strconv.Atoi(field)
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("process %d is still running after the run ended", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if _, err := callTool(t, d, "stub", "echo", nil); err == nil {
		t.Fatal("a closed door answered")
	}
}

func TestTheDoorReachesTheSourcesServer(t *testing.T) {
	l := newLykeion(t)
	d := openDoor(t, l, t.TempDir(), nil)
	got, err := callTool(t, d, "sources", "search_literature", map[string]any{"database": "openalex", "query": "insulin"})
	if err != nil {
		t.Fatal(err)
	}
	// recorded as a call, so the agent can cite it and its quotes can be matched
	if !strings.HasPrefix(got, "call:c1\n") || !strings.Contains(got, "search_literature insulin") {
		t.Fatalf("answer %q", got)
	}
	if len(l.posts) != 1 || l.posts[0]["server"] != "sources" || l.posts[0]["tool"] != "search_literature" || l.sourceAuth[0] != "Bearer ltk_run" {
		t.Fatalf("posts %v, sources auth %v", l.posts, l.sourceAuth)
	}
	// only the literature and dataset tools; a catalogue fetch names its connector
	for _, tool := range []string{"list_connectors", "fetch"} {
		if _, err := callTool(t, d, "sources", tool, nil); err == nil || !strings.Contains(err.Error(), "sources has no tool") {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	if len(l.sourceAuth) != 1 {
		t.Fatalf("the sources server was called %d times", len(l.sourceAuth))
	}
}

func TestATeamURLConnectorReachesOnlyPublicHTTPS(t *testing.T) {
	var hits atomic.Int32
	plain := httptest.NewServer(labMCP(&hits))
	t.Cleanup(plain.Close)
	tls := httptest.NewTLSServer(labMCP(&hits))
	t.Cleanup(tls.Close)
	l := newLykeion(t)
	d := openDoor(t, l, t.TempDir(), []pack.Connector{{Name: "plain", URL: plain.URL}, {Name: "local", URL: tls.URL}}, agent.MCPServer{Name: "addon", URL: plain.URL})
	if _, err := callTool(t, d, "plain", "lookup", nil); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("http Team server: %v", err)
	}
	if _, err := callTool(t, d, "local", "lookup", nil); err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("loopback Team server: %v", err)
	}
	if hits.Load() != 0 || len(l.posts) != 0 {
		t.Fatalf("%d requests reached a refused server, %d posts", hits.Load(), len(l.posts))
	}
	// an add-on server keeps the plain client
	if got, err := callTool(t, d, "addon", "lookup", map[string]any{"q": "TP53"}); err != nil || got != "call:c1\ngene TP53" {
		t.Fatalf("add-on: %q %v", got, err)
	}
}

func TestACallIsBoundedInTimeAndSize(t *testing.T) {
	defer func(t0 time.Duration, m int) { callTimeout, maxAnswer = t0, m }(callTimeout, maxAnswer)
	callTimeout, maxAnswer = 300*time.Millisecond, 1000
	l := newLykeion(t)
	d := openDoor(t, l, t.TempDir(), nil, agent.MCPServer{Name: "lab", URL: labServer(t)})
	start := time.Now()
	if _, err := callTool(t, d, "lab", "slow", nil); err == nil || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("a slow call: %v after %v", err, time.Since(start))
	}
	if _, err := callTool(t, d, "lab", "big", map[string]any{"n": 1001}); err == nil || !strings.Contains(err.Error(), "ask for less") {
		t.Fatalf("a big answer: %v", err)
	}
	if got, err := callTool(t, d, "lab", "big", map[string]any{"n": 1000}); err != nil || len(got) != len("call:c1\n")+1000 {
		t.Fatalf("an answer at the cap: %d %v", len(got), err)
	}
	if len(l.posts) != 1 {
		t.Fatalf("%d posts", len(l.posts))
	}
}

func TestTheRunEndsWhileAConnectorHangs(t *testing.T) {
	// sleep never answers MCP's initialize
	d := openDoor(t, newLykeion(t), t.TempDir(), []pack.Connector{{Name: "hung", Command: []string{"sleep", "60"}}})
	go callTool(t, d, "hung", "x", nil)
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	d.Close()
	if time.Since(start) > 5*time.Second {
		t.Fatalf("closing took %v", time.Since(start))
	}
}

func TestTheCallTokenNeverReachesTheCLI(t *testing.T) {
	f := fixture(t)
	seen := t.TempDir()
	wrapper := filepath.Join(t.TempDir(), "claude")
	// the CLI waits while its run's tokens are checked: they go when it ends
	must(t, os.WriteFile(wrapper, []byte("#!/bin/sh\ncase \"$*\" in *--mcp-config*) ;; *) exec "+f.cfg.Harnesses["claude"]+" \"$@\";; esac\nprev=\nfor a in \"$@\"; do [ \"$prev\" = --mcp-config ] && cat \"$a\" > "+seen+"/mcp; prev=$a; done\nenv > "+seen+"/env.tmp && mv "+seen+"/env.tmp "+seen+"/env\n"+
		"while [ ! -f "+seen+"/go ]; do sleep 0.05; done\nexec "+f.cfg.Harnesses["claude"]+" \"$@\"\n"), 0700))
	f.cfg.Harnesses["claude"] = wrapper
	cancel, done := startDaemon(t, f.cfg)
	defer stopDaemon(t, cancel, done)
	defer os.WriteFile(filepath.Join(seen, "go"), nil, 0600)
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(seen, "env")); return err == nil })
	env, err := os.ReadFile(filepath.Join(seen, "env"))
	must(t, err)
	cfg, err := os.ReadFile(filepath.Join(seen, "mcp"))
	must(t, err)
	if !strings.Contains(string(env), "LYK_DOOR_TOKEN=") || !strings.Contains(string(cfg), `"lyk_door"`) {
		t.Fatalf("the CLI was not given the door: %s", cfg)
	}
	var calls int
	must(t, f.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM agent_run_token WHERE agent_run_id = $1 AND scopes = '{call:record}'`, f.id).Scan(&calls))
	if calls != 1 {
		t.Fatalf("%d call tokens minted", calls)
	}
	// every run token the CLI can see is checked against what it may do
	tokens := regexp.MustCompile(`ltk_[A-Za-z0-9_-]+`).FindAllString(string(env)+string(cfg), -1)
	if len(tokens) == 0 {
		t.Fatal("no run token reached the CLI")
	}
	for _, tok := range tokens {
		var scopes []string
		err := f.env.Owner.QueryRow(context.Background(), `SELECT scopes FROM agent_run_token WHERE token_hash = $1`, auth.Hash(tok)).Scan(&scopes)
		if err == nil && slices.Contains(scopes, service.ScopeCall) {
			t.Fatalf("the CLI holds the call token")
		}
	}
}
