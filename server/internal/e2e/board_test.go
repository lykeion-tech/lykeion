package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/daemon"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type boardLab struct {
	env    *testutil.Env
	st     *store.Store
	engine *flow.Engine
	ap     *autoresearch.Service
	hosted *sources.Hosted
	h      *handler.H
	url    string
	ws     uuid.UUID
	owner  uuid.UUID
	token  string
	home   string
}

func buildLyk(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "lyk"), "../../cmd/lyk").CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func fakeJev(t *testing.T) string {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State     string
			Questions map[string]struct{ Criteria map[string]string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		choice := "supports"
		switch q := body.Questions["q"].Criteria; {
		case q["others_work"] != "":
			choice = "own_result"
		case q["first"] != "":
			choice = "first"
		case q["relevant"] != "":
			choice = "relevant"
		case q["states_it"] != "":
			choice = "does_not"
		case q["biological_replicates"] != "":
			choice = "biological_replicates"
			if strings.Contains(body.State, "Observations are matched pairs from the same subject") {
				choice = "paired_samples"
			}
		case strings.Contains(body.State, "no effect") != strings.Contains(body.State, "does not"):
			choice = "challenges"
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": map[string]any{
			"q": map[string]any{"choice": choice, "probabilities": map[string]float64{choice: 0.9}}}})
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func newBoardLab(t *testing.T) *boardLab {
	buildLyk(t)
	t.Setenv("HOME", filepath.Dir(t.TempDir())) // Keep fixture executables readable without exposing the developer's home.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if runtime.GOOS == "linux" {
		must(t, os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".credentials.json"), []byte("test-signin-fixture"), 0600))
	}
	t.Setenv("CODEX_HOME", "")
	env := testutil.Setup(t)
	l := &boardLab{env: env, st: &store.Store{Pool: env.App}, ws: uuid.New(), home: t.TempDir()}
	hub := &realtime.Hub{Store: l.st, Redis: env.Redis}
	l.st.Publish = hub.Publish
	blobs := storage.Local{Dir: t.TempDir()}
	runs := &service.AgentRuns{Store: l.st}
	l.engine = &flow.Engine{Store: l.st, AgentRuns: runs, Blobs: blobs, Jev: &jev.Client{BaseURL: fakeJev(t), Key: "k", Model: jev.Model}}
	l.ap = &autoresearch.Service{Store: l.st, AgentRuns: runs}
	runs.OnSettlement = l.ap.OnSettlement(l.engine.OnSettlement)
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/esearch.fcgi" || r.URL.Query().Get("term") != "X Y mice" {
			t.Errorf("unexpected GEO query: %s", r.URL)
			http.Error(w, "unexpected query", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"esearchresult":{"idlist":[]}}`))
	}))
	t.Cleanup(geo.Close)
	l.hosted = &sources.Hosted{Store: l.st, Blobs: blobs, GEO: sources.GEO{Base: geo.URL, HTTP: geo.Client()}}
	iss := auth.Issuer{Secret: []byte("s"), TTL: time.Hour}
	l.h = &handler.H{Store: l.st, JWT: iss, Limit: auth.Limiter{R: env.Redis}, Hub: hub,
		Autoresearch: l.ap, AgentRuns: runs, Blobs: blobs, Engine: l.engine, Sources: l.hosted.Handler()}
	srv := httptest.NewServer(handler.NewRouter(l.h))
	t.Cleanup(srv.Close)
	runs.MCPURL = srv.URL + "/mcp/sources"
	l.url = srv.URL

	ctx := context.Background()
	u, err := l.st.Global().UpsertUserByEmail(ctx, "a@lab.org")
	must(t, err)
	l.owner = u.ID
	must(t, l.st.InWorkspace(ctx, l.ws, u.ID, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: l.ws, Slug: "lab", Name: "Lab"}); err != nil {
			return err
		}
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: l.ws, UserID: u.ID, Role: "owner"})
	}))
	// The daemon routes take only a personal lyk_ token, never a session.
	jwt, err := iss.Issue(u.ID, u.Email)
	must(t, err)
	var tok struct{ Token string }
	must(t, (&apiclient.Client{Server: l.url, Token: jwt}).Do(ctx, "POST", "/api/tokens", map[string]string{"name": "board"}, &tok))
	if !strings.HasPrefix(tok.Token, "lyk_") {
		t.Fatalf("not a personal token: %q", tok.Token)
	}
	l.token = tok.Token
	return l
}

func (l *boardLab) run(t *testing.T) context.CancelFunc {
	return l.runOn(t, "claude", "testdata/fake-claude.sh")
}

// runOn starts a daemon with one harness and the engine tick.
func (l *boardLab) runOn(t *testing.T, harness, script string) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	source, err := os.ReadFile(script)
	must(t, err)
	fake := filepath.Join(t.TempDir(), harness)
	must(t, os.WriteFile(fake, source, 0700))
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Server: l.url, Token: l.token, StateDir: t.TempDir(), Home: l.home, Device: "test",
			Harnesses: map[string]string{harness: fake}, Slots: 5, Poll: 200 * time.Millisecond, Heartbeat: time.Second, Flush: 200 * time.Millisecond})
	}()
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		tick := time.NewTicker(300 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				l.engine.Tick(ctx)
				if err := l.ap.Tick(ctx); err != nil && ctx.Err() == nil {
					t.Errorf("autoresearch tick: %v", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		select {
		case <-tickDone:
		case <-time.After(8 * time.Second):
			t.Error("engine shutdown did not join")
		}
		select {
		case err := <-done:
			must(t, err)
		case <-time.After(8 * time.Second):
			t.Error("daemon shutdown did not join")
		}
	}
}

func (l *boardLab) card(t *testing.T, id uuid.UUID) db.Card {
	var c db.Card
	must(t, l.st.InWorkspace(context.Background(), l.ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		c, err = tx.Q.GetCard(context.Background(), db.GetCardParams{WorkspaceID: l.ws, ID: id})
		return err
	}))
	return c
}

func (l *boardLab) waitCard(t *testing.T, id uuid.UUID, what string, cond func(db.Card) bool) db.Card {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if c := l.card(t, id); cond(c) {
			return c
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %+v", what, l.card(t, id))
	return db.Card{}
}

type boardSourceBearer string

func (b boardSourceBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(copy)
}

// TestMetadataMCPHelper is invoked only by the fake provider. It uses the
// daemon's actual role credential and sources endpoint, never an owner token.
func TestMetadataMCPHelper(t *testing.T) {
	if os.Getenv("LYK_TEST_MCP_CALL") == "" {
		return
	}
	ctx := context.Background()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "fake-provider", Version: "1"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: os.Getenv("LYK_SERVER") + "/mcp/sources", HTTPClient: &http.Client{Transport: boardSourceBearer(os.Getenv("LYK_TOKEN"))}}, nil)
	must(t, err)
	defer client.Close()
	var args map[string]any
	must(t, json.Unmarshal([]byte(os.Getenv("LYK_TEST_MCP_ARGS")), &args))
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: os.Getenv("LYK_TEST_MCP_CALL"), Arguments: args})
	must(t, err)
	if result.IsError {
		t.Fatalf("MCP tool failed: %+v", result)
	}
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			t.Log(text.Text)
		}
	}
}
