package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/daemon"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/round"
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
	engine *round.Engine
	hosted *sources.Hosted
	url    string
	ws     uuid.UUID
	owner  uuid.UUID
	token  string
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
		if body.Questions["q"].Criteria["others_work"] != "" {
			choice = "own_result"
		} else if body.Questions["q"].Criteria["biological_replicates"] != "" {
			choice = "biological_replicates"
		} else if strings.Contains(body.State, "no effect") != strings.Contains(body.State, "does not") {
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
	t.Setenv("HOME", t.TempDir()) // continuity runs the CLI on HOME; keep the developer's own config out
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	env := testutil.Setup(t)
	l := &boardLab{env: env, st: &store.Store{Pool: env.App}, ws: uuid.New()}
	hub := &realtime.Hub{Store: l.st, Redis: env.Redis}
	l.st.Publish = hub.Publish
	blobs := storage.Local{Dir: t.TempDir()}
	runs := &service.AgentRuns{Store: l.st}
	l.engine = &round.Engine{Store: l.st, AgentRuns: runs, Blobs: blobs, JevKey: "k", JevURL: fakeJev(t)}
	runs.OnSettled = l.engine.OnSettled
	l.hosted = &sources.Hosted{Store: l.st, Blobs: blobs}
	iss := auth.Issuer{Secret: []byte("s"), TTL: time.Hour}
	srv := httptest.NewServer(handler.NewRouter(&handler.H{Store: l.st, JWT: iss, Limit: auth.Limiter{R: env.Redis}, Hub: hub,
		AgentRuns: runs, Blobs: blobs, Kinds: round.Kinds(), Engine: l.engine, Sources: l.hosted.Handler()}))
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
		if err := tx.Q.SetWorkspaceSettings(ctx, db.SetWorkspaceSettingsParams{ID: l.ws, Settings: []byte(`{"jev":{"enabled":true}}`)}); err != nil {
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

// startCard creates a card, lets WIP pull start it, and records the sources
// its roles would have fetched in round 1.
func (l *boardLab) startCard(t *testing.T) uuid.UUID {
	ctx := context.Background()
	var card uuid.UUID
	must(t, l.st.InWorkspace(ctx, l.ws, l.owner, func(tx *store.Tx) error {
		c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: l.ws, Number: 1, Title: "t", Question: "Does X raise Y?", OwnerID: l.owner, FollowsFrom: []uuid.UUID{}})
		card = c.ID
		return err
	}))
	// Each source was fetched by an earlier run. Those runs sit in round 0, so
	// the engine's view of round 1 neither counts them nor skips a role for them.
	// Each runs, with its token, only while the sources server records its call.
	rec := func(name, kind, key string, body map[string]any, text string) {
		run, token := uuid.New(), "ltk_"+uuid.NewString()
		if _, err := l.env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
			VALUES ($1, $2, $3, $4, 'grounding', 0, $5, 'claude', 'p', 'running', 1)`, run, l.ws, card, l.owner, name); err != nil {
			t.Fatal(err)
		}
		if _, err := l.env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
			VALUES ($1, $2, $3, $4, $5, 1, now() + interval '1 hour')`, auth.Hash(token), l.ws, run, card, name); err != nil {
			t.Fatal(err)
		}
		role := middleware.Role{AgentRunID: run, WorkspaceID: l.ws, CardID: card, Role: name, Column: "grounding", Round: 0}
		if err := l.hosted.Record(ctx, role, token, kind, key, body, text); err != nil {
			t.Fatal(err)
		}
		if _, err := l.env.Owner.Exec(ctx, `UPDATE agent_run SET status = 'completed', completed_at = now() WHERE id = $1`, run); err != nil {
			t.Fatal(err)
		}
	}
	rec(ground.Literature, "search", "", map[string]any{"database": "europepmc", "query": "x y"}, "")
	rec(ground.Contrarian, "search", "", map[string]any{"database": "openalex", "query": "x no effect"}, "")
	rec(ground.Literature, "fetch", "europepmc:PMC1", map[string]any{"source_id": "europepmc:PMC1", "full_text": true}, "In treated mice, X raised Y by 40% over controls.")
	rec(ground.Contrarian, "fetch", "openalex:W2", map[string]any{"source_id": "openalex:W2"}, "We found no effect of X on Y in rats.")
	rec(ground.DataScout, "open_dataset", "GSE1", map[string]any{"accession": "GSE1"}, "accession: GSE1\n- GSM1: mouse 1\n- GSM2: mouse 2\n")
	return card
}

func (l *boardLab) run(t *testing.T) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	fake, err := filepath.Abs("testdata/fake-claude.sh")
	must(t, err)
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Server: l.url, Token: l.token, StateDir: t.TempDir(), Home: t.TempDir(), Device: "test",
			Harnesses: map[string]string{"claude": fake}, Slots: 5, Poll: 200 * time.Millisecond, Heartbeat: time.Second, Flush: 200 * time.Millisecond})
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
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if c := l.card(t, id); cond(c) {
			return c
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %+v", what, l.card(t, id))
	return db.Card{}
}

func TestGroundingPassesAndTheCardMovesToHypotheses(t *testing.T) {
	l := newBoardLab(t)
	card := l.startCard(t)
	defer l.run(t)()
	c := l.waitCard(t, card, "Hypotheses", func(c db.Card) bool { return c.BoardColumn == "hypotheses" })
	if c.Round != 2 || c.State != "working" {
		t.Fatalf("%+v", c)
	}
	var verdict string
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT body->>'verdict' FROM record WHERE card_id = $1 AND kind = 'gate'`, card).Scan(&verdict))
	if verdict != "pass" {
		t.Fatalf("gate verdict %q", verdict)
	}
}

func TestParaphraseRerunsOnlyTheLiteratureScout(t *testing.T) {
	// the fake literature scout paraphrases in round 1 and quotes exactly after
	t.Setenv("LYK_TEST_PARAPHRASE_ROUND1", "1")
	argv := filepath.Join(t.TempDir(), "argv")
	t.Setenv("LYK_TEST_ARGV", argv)
	l := newBoardLab(t)
	card := l.startCard(t)
	defer l.run(t)()
	l.waitCard(t, card, "Hypotheses", func(c db.Card) bool { return c.BoardColumn == "hypotheses" })
	var roles []string
	rows, err := l.env.Owner.Query(context.Background(), `SELECT role FROM agent_run WHERE card_id = $1 AND board_column = 'grounding' AND round = 2 ORDER BY created_at`, card)
	must(t, err)
	defer rows.Close()
	for rows.Next() {
		var r string
		must(t, rows.Scan(&r))
		roles = append(roles, r)
	}
	must(t, rows.Err())
	if strings.Join(roles, ",") != "literature-scout,grounding-reviewer" {
		t.Fatalf("round 2 ran %v", roles)
	}
	var note, prompt string
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT handoff_note, prompt FROM agent_run WHERE card_id = $1 AND round = 2 AND role = 'literature-scout'`, card).Scan(&note, &prompt))
	if !strings.Contains(note, "quote not found verbatim") || !strings.Contains(prompt, note) {
		t.Fatalf("the gate's reason did not reach the redone scout: %q", note)
	}
	var stale int
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'evidence' AND role = 'literature-scout' AND superseded_at IS NOT NULL`, card).Scan(&stale))
	if stale != 1 {
		t.Fatalf("the paraphrased evidence was not superseded: %d", stale)
	}
	// the redone scout continues its round-1 session; the reviewer never resumes
	b, err := os.ReadFile(argv)
	must(t, err)
	var scout, reviewer []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		switch {
		case strings.HasPrefix(line, "literature-scout "):
			scout = append(scout, line)
		case strings.HasPrefix(line, "grounding-reviewer "):
			reviewer = append(reviewer, line)
		}
	}
	if len(scout) != 2 || strings.Contains(scout[0], "--resume") || !strings.HasSuffix(scout[1], "--resume s") {
		t.Fatalf("the literature scout's launches: %q", scout)
	}
	if len(reviewer) == 0 {
		t.Fatalf("reviewer launches: %q", reviewer)
	}
	for _, line := range reviewer {
		if strings.Contains(line, "--resume") {
			t.Fatalf("a reviewer resumed: %q", line)
		}
	}
	var turns int
	var cost float64
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT count(*), sum(u.cost_usd) FROM agent_run_usage u JOIN agent_run r ON r.id = u.agent_run_id WHERE r.card_id = $1 AND r.role = 'literature-scout'`, card).Scan(&turns, &cost))
	if turns != 2 || cost < 0.019999 || cost > 0.020001 {
		t.Fatalf("resumed scout usage: turns=%d cost=%g", turns, cost)
	}
	var scopes int
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT count(DISTINCT continuity_scope) FROM agent_run WHERE card_id = $1 AND role = 'literature-scout' AND round > 0`, card).Scan(&scopes))
	if scopes != 1 {
		t.Fatalf("the literature scout ran under %d scopes", scopes)
	}
}

func TestNoDataStopsTheCard(t *testing.T) {
	l := newBoardLab(t)
	nodata := filepath.Join(t.TempDir(), "nodata")
	must(t, os.WriteFile(nodata, nil, 0o644))
	t.Setenv("LYK_TEST_NODATA", nodata)
	card := l.startCard(t)
	defer l.run(t)()
	c := l.waitCard(t, card, "stopped", func(c db.Card) bool { return c.Ending == "stopped" })
	if c.BoardColumn != "done" || !strings.Contains(c.EndReason, "no usable data") {
		t.Fatalf("%+v", c)
	}
}
