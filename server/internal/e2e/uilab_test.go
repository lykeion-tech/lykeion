//go:build unix

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/board"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/daemon"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// labEmail is the person the dashboard's browser tests sign in as; the lab's
// daemon runs that person's agent runs.
const labEmail = "ana@lab.test"

// codeMail keeps the last sign-in code sent to each address, so the browser
// tests can read it the way a person reads their mail.
type codeMail struct {
	mu    sync.Mutex
	codes map[string]string
}

var sixDigits = regexp.MustCompile(`\b\d{6}\b`)

func (m *codeMail) Send(_ context.Context, to, subject, body string) error {
	if subject != "Your Lykeion sign-in code" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[to] = sixDigits.FindString(body)
	return nil
}

// TestUILab serves a whole lab for the dashboard's browser tests: the real
// router, engine and realtime hub, and a daemon whose claude is a script. It
// runs only when LYK_UI_LAB names the address to listen on, and serves until
// the process is interrupted.
func TestUILab(t *testing.T) {
	addr := os.Getenv("LYK_UI_LAB")
	if addr == "" {
		t.Skip("set LYK_UI_LAB=host:port to serve the dashboard's browser-test lab")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("LYK_UI_LAB must name a loopback IP and port")
	}
	buildLyk(t)
	// Explicit opt-in keeps real Codex sign-in for a sandboxed provider check.
	// The daemon state and synthetic fixture skills remain disposable.
	realCodex := os.Getenv("LYK_UI_REAL_CODEX") == "1"
	if !realCodex {
		t.Setenv("HOME", filepath.Dir(t.TempDir())) // Keep fixture executables readable without exposing the developer's home.
		t.Setenv("CODEX_HOME", "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("LYK_TOKEN", "")
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "lyk.json"))
	// Published-source responses stay outside the product API and use its real parser.
	lateSourceRelease, lateSourceStarted := make(chan struct{}), make(chan struct{})
	var lateReleaseOnce, lateStartedOnce sync.Once
	previousHTTP := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: uiSkillSourceTransport{fallback: previousHTTP.Transport, release: lateSourceRelease, started: lateSourceStarted, startedOnce: &lateStartedOnce}}
	t.Cleanup(func() { lateReleaseOnce.Do(func() { close(lateSourceRelease) }); http.DefaultClient = previousHTTP })
	fixtureSkillHome := os.Getenv("HOME")
	if realCodex {
		fixtureSkillHome = t.TempDir()
	}
	for _, name := range []string{"runtime-review", "runtime-duplicate", "runtime-single"} {
		dir := filepath.Join(fixtureSkillHome, ".claude", "skills", name)
		must(t, os.MkdirAll(filepath.Join(dir, "references"), 0700))
		must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Runtime folder fixture.\n---\n\n# Runtime review\n"), 0600))
		must(t, os.WriteFile(filepath.Join(dir, "references", "raw.bin"), []byte{0, 255, 128, 13, 10}, 0600))
	}
	env := testutil.Setup(t)
	base := "http://" + addr

	st := &store.Store{Pool: env.App}
	hub := &realtime.Hub{Store: st, Redis: env.Redis}
	st.Publish = hub.Publish
	blobs := storage.Local{Dir: t.TempDir()}
	runs := &service.AgentRuns{Store: st, MCPURL: base + "/mcp/sources"}
	engine := &flow.Engine{Store: st, AgentRuns: runs, Blobs: blobs}
	ap := &autoresearch.Service{Store: st, AgentRuns: runs, Engine: engine, Key: []byte("0123456789abcdef0123456789abcdef")}
	runs.OnSettlement = ap.OnSettlement(engine.OnSettlement)
	hosted := &sources.Hosted{Store: st, Blobs: blobs}
	mail := &codeMail{codes: map[string]string{}}
	iss := auth.Issuer{Secret: []byte("ui-lab-signing-secret-0123456789"), TTL: 24 * time.Hour}

	h := &handler.H{Store: st, JWT: iss, Mail: mail, Limit: auth.Limiter{R: env.Redis},
		Hub: hub, Autoresearch: ap, AgentRuns: runs, Blobs: blobs, Engine: engine, Sources: hosted.Handler(),
		Connectors: &connectors.Client{Secret: []byte("0123456789abcdef0123456789abcdef")}}
	if dir := os.Getenv("LYK_TEST_SHIPPED_DIR"); dir != "" {
		// Browser tests ship and replace Team versions on disk while the lab runs.
		h.Shipped = os.DirFS(dir)
	}
	if realCodex {
		runs.LabMCPURL = base + "/mcp/lab"
		runs.OnSettlement = (labmemory.Answers{Store: st}).SettlementHook(runs.OnSettlement)
		h.Lab = (labmemory.Tools{Store: st, Blobs: blobs}).Handler()
	}
	mux := http.NewServeMux()
	mux.Handle("/", handler.NewRouter(h))
	sourceFixtureAuth := func(w http.ResponseWriter, r *http.Request) bool {
		cookie, err := r.Cookie("lyk_session")
		if err != nil {
			http.Error(w, "sign in required", 401)
			return false
		}
		_, email, err := iss.Parse(cookie.Value)
		if err != nil || email != labEmail {
			http.Error(w, "fixture owner required", 403)
			return false
		}
		return true
	}
	// Independent fixture sign-ins share the proxy's loopback address. Only the
	// lab owner can reset that address's two counters; email budgets stay intact.
	mux.HandleFunc("POST /lab/auth-ip/reset", func(w http.ResponseWriter, r *http.Request) {
		if !sourceFixtureAuth(w, r) {
			return
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(ip).IsLoopback() {
			http.Error(w, "loopback required", 403)
			return
		}
		if err := env.Redis.Del(r.Context(), "lyk:rl:code:ip:"+ip, "lyk:rl:verify:ip:"+ip).Err(); err != nil {
			http.Error(w, "fixture reset failed", 500)
			return
		}
		w.WriteHeader(204)
	})

	uiTeamFixture(t, mux, h, iss)
	uiConnectorFixtures(t, mux, h, base, sourceFixtureAuth)
	uiAutoresearchFixtures(mux, st, sourceFixtureAuth)
	mux.HandleFunc("GET /lab/skill-source", func(w http.ResponseWriter, r *http.Request) {
		if !sourceFixtureAuth(w, r) {
			return
		}
		started := false
		select {
		case <-lateSourceStarted:
			started = true
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"started": started})
	})
	mux.HandleFunc("POST /lab/skill-source/release", func(w http.ResponseWriter, r *http.Request) {
		if !sourceFixtureAuth(w, r) {
			return
		}
		lateReleaseOnce.Do(func() { close(lateSourceRelease) })
		w.WriteHeader(204)
	})

	mux.HandleFunc("GET /lab/code", func(w http.ResponseWriter, r *http.Request) {
		mail.mu.Lock()
		code := mail.codes[strings.ToLower(r.URL.Query().Get("email"))]
		mail.mu.Unlock()
		if code == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(code))
	})
	mux.HandleFunc("POST /lab/hypotheses/{card}", func(w http.ResponseWriter, r *http.Request) {
		card, err := uuid.Parse(r.PathValue("card"))
		if err != nil {
			http.Error(w, "no such card", 404)
			return
		}
		var ws uuid.UUID
		if err := env.Owner.QueryRow(r.Context(), "SELECT workspace_id FROM card WHERE id=$1", card).Scan(&ws); err != nil {
			http.Error(w, "no such card", 404)
			return
		}
		err = st.InWorkspace(r.Context(), ws, uuid.Nil, func(tx *store.Tx) error {
			if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
				return err
			}
			if err := tx.Q.WaitLockCard(r.Context(), card); err != nil {
				return err
			}
			c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{ID: card, WorkspaceID: ws})
			if err != nil {
				return err
			}
			s := board.FromCard(c)
			s.Column, s.State, s.Flag, s.Rounds, s.Round = board.Hypotheses, board.Working, "", 0, c.Round+1
			if err := tx.Q.SaveCardBoard(r.Context(), s.Params(card, ws)); err != nil {
				return err
			}
			return tx.Emit(r.Context(), "card.steered", &card, store.System, map[string]string{"column": "hypotheses"})
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Many long records, as a busy round leaves them, written straight into the
	// card's current column and round; the dashboard's layout tests read them.
	mux.HandleFunc("POST /lab/records/{card}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.URL.Query().Get("n"))
		if err != nil || n < 1 || n > 200 {
			http.Error(w, "n between 1 and 200", http.StatusBadRequest)
			return
		}
		claim := strings.Repeat("a long claim about islet amyloid ", 12) + strings.Repeat("y", 300)
		tag, err := env.Owner.Exec(r.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
			SELECT c.workspace_id, c.id, c.board_column, c.round, 'evidence', 'literature-scout', 'E' || g,
			  jsonb_build_object('claim', $2::text, 'source_id', 'europepmc:PMC' || g)
			FROM card c, generate_series(1, $3::int) g WHERE c.id = $1`, r.PathValue("card"), claim, n)
		if err != nil || tag.RowsAffected() != int64(n) {
			http.Error(w, "no such card", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Real stored history with enough non-rendered events to cross a page boundary.
	mux.HandleFunc("POST /lab/events/{card}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("card"))
		n, countErr := strconv.Atoi(r.URL.Query().Get("n"))
		if err != nil || countErr != nil || n < 1 || n > 1000 {
			http.Error(w, "invalid history fixture", 400)
			return
		}
		var ws uuid.UUID
		if err := env.Owner.QueryRow(r.Context(), "SELECT workspace_id FROM card WHERE id=$1", id).Scan(&ws); err != nil {
			http.Error(w, "no such card", 404)
			return
		}
		err = st.InWorkspace(r.Context(), ws, uuid.Nil, func(tx *store.Tx) error {
			for i := 0; i < n; i++ {
				if err := tx.Emit(r.Context(), "record.created", &id, store.System, map[string]any{"fixture": i}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Drives only a disposable fixture run through the real lifecycle queries,
	// with no agent execution or scientific output.
	mux.HandleFunc("POST /lab/run/{run}/{action}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("run"))
		if err != nil {
			http.Error(w, "no such run", 404)
			return
		}
		var ws uuid.UUID
		if err := env.Owner.QueryRow(r.Context(), "SELECT workspace_id FROM agent_run WHERE id=$1", id).Scan(&ws); err != nil {
			http.Error(w, "no such run", 404)
			return
		}
		err = st.InWorkspace(r.Context(), ws, uuid.Nil, func(tx *store.Tx) error {
			run, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: ws, ID: id})
			if err != nil || run.RuntimeID == nil {
				return fmt.Errorf("no fixture runtime")
			}
			if r.PathValue("action") == "start" {
				if _, err := tx.Q.TouchRuntime(r.Context(), db.TouchRuntimeParams{WorkspaceID: ws, ID: *run.RuntimeID, OwnerID: run.ResponsibleUserID}); err != nil {
					return err
				}
				if err := tx.Q.RequeueAgentRun(r.Context(), db.RequeueAgentRunParams{WorkspaceID: ws, ID: id}); err != nil {
					return err
				}
				if run.CardID == nil {
					return fmt.Errorf("fixture run has no card")
				}
				c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{WorkspaceID: ws, ID: *run.CardID})
				if err != nil {
					return err
				}
				state := board.FromCard(c)
				state.Flag = ""
				if err := tx.Q.SaveCardBoard(r.Context(), state.Params(c.ID, ws)); err != nil {
					return err
				}
				claimed, err := tx.Q.ClaimAgentRun(r.Context(), db.ClaimAgentRunParams{WorkspaceID: ws, RuntimeID: run.RuntimeID})
				if err != nil || claimed.ID != id {
					return fmt.Errorf("fixture claim: %v", err)
				}
				if _, err := tx.Q.StartAgentRun(r.Context(), db.StartAgentRunParams{WorkspaceID: ws, ID: id, RuntimeID: run.RuntimeID}); err != nil {
					return err
				}
				state.Flag = c.Flag
				if err := tx.Q.SaveCardBoard(r.Context(), state.Params(c.ID, ws)); err != nil {
					return err
				}
				return tx.Emit(r.Context(), "agent_run.started", run.CardID, store.System, map[string]any{"id": id})
			}
			if r.PathValue("action") != "finish" {
				return fmt.Errorf("unknown fixture action")
			}
			failure := "agent_error"
			if err := tx.Q.FinishAgentRun(r.Context(), db.FinishAgentRunParams{WorkspaceID: ws, ID: id, Status: "failed", Error: "fixture lifecycle stopped", FailureReason: &failure}); err != nil {
				return err
			}
			return tx.Emit(r.Context(), "agent_run.failed", run.CardID, store.System, map[string]any{"id": id})
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Creates a study no Team or agent works, held as a column's finished
	// work, so fixtures can stage it without a run starting. The API always
	// gives a new study its Teams.
	mux.HandleFunc("POST /lab/idle-study", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("lyk_session")
		if err != nil {
			http.Error(w, "sign in required", 401)
			return
		}
		user, _, err := iss.Parse(cookie.Value)
		if err != nil {
			http.Error(w, "sign in required", 401)
			return
		}
		ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
		if err != nil {
			http.Error(w, "workspace required", 400)
			return
		}
		var in struct {
			Title, Question string
			AgentID         *uuid.UUID `json:"agent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Question == "" {
			http.Error(w, "question required", 400)
			return
		}
		if in.Title == "" {
			in.Title = in.Question
		}
		var kind *string
		if in.AgentID != nil {
			agent := "agent"
			kind = &agent
		}
		var c db.Card
		err = st.InWorkspace(r.Context(), ws, user, func(tx *store.Tx) error {
			n, err := tx.Q.NextCardNumber(r.Context(), ws)
			if err != nil {
				return err
			}
			c, err = tx.Q.CreateCard(r.Context(), db.CreateCardParams{WorkspaceID: ws, Number: n, OwnerID: user,
				Title: in.Title, Question: in.Question, BoardColumn: board.Grounding,
				ScopeFrom: board.Grounding, ScopeTo: board.Validation, Round: 1, AssigneeType: kind, AssigneeID: in.AgentID})
			if err != nil {
				return err
			}
			s := board.FromCard(c)
			s.State = "done"
			if err := tx.Q.SaveCardBoard(r.Context(), s.Params(c.ID, ws)); err != nil {
				return err
			}
			return tx.Emit(r.Context(), "card.created", &c.ID, store.User(user), map[string]any{"number": c.Number, "title": c.Title})
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": c.ID, "number": c.Number, "title": c.Title})
	})

	// Puts a study in a column and state and writes the flow's records into it,
	// so the study page can show scores, steps and calls without running a Team.
	mux.HandleFunc("POST /lab/flow-records/{card}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Column    string `json:"column"`
			State     string `json:"state"`
			BlockKind string `json:"block_kind"`
			Redos     int    `json:"redos"`
			Round     int    `json:"round"`
			ScopeFrom string `json:"scope_from"`
			Workflow  string `json:"workflow"`
			Records   []struct {
				Kind string         `json:"kind"`
				Key  string         `json:"key"`
				Body map[string]any `json:"body"`
				// Run gives the record a finished agent run of its own.
				Run bool `json:"run"`
			} `json:"records"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Column == "" || in.Round < 1 {
			http.Error(w, "column, state and round are required", http.StatusBadRequest)
			return
		}
		// scope_from, when given, keeps the earlier columns in the study's range;
		// workflow, when given, makes it a Workflow study.
		tag, err := env.Owner.Exec(r.Context(), `UPDATE card SET board_column = $2, scope_from = COALESCE(NULLIF($7, ''), $2), scope_to = $2,
			state = $3, block_kind = $4, redos = $5, round = $6, flag = '', workflow = COALESCE(NULLIF($8, ''), workflow) WHERE id = $1`,
			r.PathValue("card"), in.Column, in.State, in.BlockKind, in.Redos, in.Round, in.ScopeFrom, in.Workflow)
		if err != nil || tag.RowsAffected() != 1 {
			http.Error(w, "no such card", http.StatusNotFound)
			return
		}
		for _, rec := range in.Records {
			body, _ := json.Marshal(rec.Body)
			var run *uuid.UUID
			if rec.Run {
				var id uuid.UUID
				if err := env.Owner.QueryRow(r.Context(), `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, completed_at)
					SELECT workspace_id, id, owner_id, board_column, round, 'leader', 'claude', 'lab hand-in', 'completed', now() FROM card WHERE id = $1 RETURNING id`,
					r.PathValue("card")).Scan(&id); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				run = &id
			}
			if _, err := env.Owner.Exec(r.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body, agent_run_id)
				SELECT workspace_id, id, board_column, round, $2, 'server', $3, $4::jsonb, $5 FROM card WHERE id = $1`,
				r.PathValue("card"), rec.Kind, rec.Key, string(body), run); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Puts an Inbox item of any kind on a study for its owner, as the flow
	// writes them, so the Inbox can show each kind without running a Team.
	mux.HandleFunc("POST /lab/inbox/{card}", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Kind, Text string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Kind == "" || in.Text == "" {
			http.Error(w, "kind and text are required", http.StatusBadRequest)
			return
		}
		tag, err := env.Owner.Exec(r.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
			SELECT workspace_id, id, owner_id, $2, $3 FROM card WHERE id = $1`, r.PathValue("card"), in.Kind, in.Text)
		if err != nil || tag.RowsAffected() != 1 {
			http.Error(w, "no such card", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Offers a study's owner a rating for a Team-worked column, as a hit draw
	// would; the lab has no Jev, so its columns never finish on their own.
	mux.HandleFunc("POST /lab/rating-offer/{card}/{column}", func(w http.ResponseWriter, r *http.Request) {
		tag, err := env.Owner.Exec(r.Context(), `INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at)
			SELECT c.workspace_id, c.id, t.board_column, t.pack_id, c.owner_id, true, now()
			FROM card c JOIN card_team t ON t.card_id = c.id WHERE c.id = $1 AND t.board_column = $2`,
			r.PathValue("card"), r.PathValue("column"))
		if err != nil || tag.RowsAffected() != 1 {
			http.Error(w, "no Team works that column of that card", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /lab/fixtures", func(w http.ResponseWriter, r *http.Request) {
		fixture, err := seedUIFixture(r.Context(), st)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(fixture)
	})

	ln, err := net.Listen("tcp", addr)
	must(t, err)
	srv := &http.Server{Handler: mux}
	serverDone := make(chan error, 1)
	go func() { serverDone <- srv.Serve(ln) }()
	defer func() {
		hub.Close()
		srv.Close()
		if err := <-serverDone; err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	u, err := st.Global().UpsertUserByEmail(ctx, labEmail)
	must(t, err)
	jwt, err := iss.Issue(u.ID, u.Email)
	must(t, err)
	// The daemon routes take only a personal lyk_ token, never a session.
	var tok struct{ Token string }
	must(t, (&apiclient.Client{Server: base, Token: jwt}).Do(ctx, "POST", "/api/tokens", map[string]string{"name": "lab daemon"}, &tok))
	script, err := os.ReadFile("testdata/ui-fake-claude.sh")
	must(t, err)
	fake := filepath.Join(t.TempDir(), "ui-fake-claude")
	must(t, os.WriteFile(fake, script, 0700))
	uiAvailabilityFixture(t, mux, daemon.Config{Server: base, Token: tok.Token, Harnesses: map[string]string{"claude": fake}, Slots: 1, Poll: 200 * time.Millisecond, Heartbeat: time.Second, Flush: 200 * time.Millisecond}, sourceFixtureAuth)
	harnesses := map[string]string{"claude": fake}
	if realCodex {
		bin, err := exec.LookPath("codex")
		must(t, err)
		harnesses = map[string]string{"codex": bin}
	}
	daemonDone := make(chan error, 1)
	daemonState, daemonHome := t.TempDir(), t.TempDir()
	go func() {
		daemonDone <- daemon.Run(ctx, daemon.Config{Server: base, Token: tok.Token, StateDir: daemonState, Home: daemonHome,
			Device: "lab-mac", Harnesses: harnesses, Slots: 4,
			Poll: 200 * time.Millisecond, Heartbeat: time.Second, Flush: 200 * time.Millisecond})
	}()

	t.Logf("lab serving on %s", base)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	sweep := time.NewTicker(30 * time.Second)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-daemonDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(15 * time.Second):
				t.Error("lab daemon shutdown did not join")
			}
			return
		case err := <-daemonDone:
			t.Fatalf("lab daemon stopped: %v", err)
		case <-sweep.C:
			if err := runs.Sweep(ctx); err != nil && ctx.Err() == nil {
				t.Logf("runtime sweep: %v", err)
			}
		case <-tick.C:
			if err := engine.Tick(ctx); err != nil && ctx.Err() == nil {
				t.Logf("engine tick: %v", err)
			}
		}
	}
}

// uiFixture names persisted rows, so browser assertions read the real APIs.
type uiFixture struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	CardID      uuid.UUID `json:"card_id"`
	RunID       uuid.UUID `json:"run_id"`
}

func seedUIFixture(ctx context.Context, st *store.Store) (uiFixture, error) {
	f := uiFixture{WorkspaceID: uuid.New()}
	u, err := st.Global().UpsertUserByEmail(ctx, labEmail)
	if err != nil {
		return f, err
	}
	err = st.InWorkspace(ctx, f.WorkspaceID, u.ID, func(tx *store.Tx) error {
		ws := f.WorkspaceID
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: ws, Slug: "fixture-" + ws.String()[:8], Name: "Browser fixtures"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws, UserID: u.ID, Role: "owner"}); err != nil {
			return err
		}
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		n, err := tx.Q.NextCardNumber(ctx, ws)
		if err != nil {
			return err
		}
		c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: "grounding", WorkspaceID: ws, Number: n, OwnerID: u.ID, Title: "Transcript fixture", Question: "What remains unresolved in the recorded evidence?"})
		if err != nil {
			return err
		}
		f.CardID = c.ID
		run, err := tx.Q.EnqueueAgentRun(ctx, db.EnqueueAgentRunParams{WorkspaceID: ws, CardID: &c.ID, ResponsibleUserID: u.ID, BoardColumn: board.Grounding, Round: 1, Role: "scoper", Harness: "claude", Prompt: "Recorded browser transcript fixture", MaxAttempts: 2})
		if err != nil {
			return err
		}
		f.RunID = run.ID
		rt, err := tx.Q.UpsertRuntime(ctx, db.UpsertRuntimeParams{WorkspaceID: ws, OwnerID: u.ID, DaemonID: "fixture", Harness: "claude", DeviceName: "Recorded transcript", CliVersion: "fixture"})
		if err != nil {
			return err
		}
		// Exercise the shipped attempt counter and sequence scope in one commit.
		for attempt := int32(1); attempt <= 2; attempt++ {
			claimed, err := tx.Q.ClaimAgentRun(ctx, db.ClaimAgentRunParams{WorkspaceID: ws, RuntimeID: &rt.ID})
			if err != nil {
				return err
			}
			if claimed.ID != run.ID || claimed.Attempt != attempt {
				return fmt.Errorf("unexpected fixture claim: %s/%d", claimed.ID, claimed.Attempt)
			}
			if _, err := tx.Q.StartAgentRun(ctx, db.StartAgentRunParams{WorkspaceID: ws, ID: run.ID, RuntimeID: &rt.ID}); err != nil {
				return err
			}
			count := 2
			if attempt == 2 {
				count = 501
			}
			for seq := 1; seq <= count; seq++ {
				text := fmt.Sprintf("Attempt %d message %03d", attempt, seq)
				body, err := json.Marshal(text)
				if err != nil {
					return err
				}
				kind, tool := "text", ""
				if attempt == 2 {
					switch seq {
					case 2:
						kind, tool = "tool_use", "recorded-tool"
						body, _ = json.Marshal(map[string]string{"input": strings.Repeat("long-input-", 40) + "END OF FULL TOOL INPUT"})
					case 3:
						kind, tool = "tool_result", "recorded-tool"
						body, _ = json.Marshal("Recorded tool refusal")
					case 4:
						kind = "status"
						body, _ = json.Marshal("Recorded continuity status")
					}
				}
				if err := tx.Q.InsertAgentRunMessage(ctx, db.InsertAgentRunMessageParams{WorkspaceID: ws, AgentRunID: run.ID, Attempt: attempt, Seq: int32(seq), Type: kind, Tool: tool, Content: body}); err != nil {
					return err
				}
			}
			if attempt == 1 {
				failure := "agent_error"
				if err := tx.Q.RequeueAgentRun(ctx, db.RequeueAgentRunParams{WorkspaceID: ws, ID: run.ID, Error: "recorded retry", FailureReason: &failure}); err != nil {
					return err
				}
			} else {
				failure := "agent_error"
				if err := tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{WorkspaceID: ws, ID: run.ID, Status: "failed", Error: "recorded failure", FailureReason: &failure}); err != nil {
					return err
				}
			}
		}
		// A flag excludes this fixture from the engine and daemon claim candidates.
		s := board.FromCard(c)
		s.Column, s.State, s.Flag, s.Round = board.Grounding, board.Working, "escalated", 1
		if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, ws)); err != nil {
			return err
		}
		return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]string{"column": board.Grounding, "flag": "escalated"})
	})
	return f, err
}

func TestUIFakeClaudeVersionHasNoRunEffects(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	must(t, os.WriteFile(filepath.Join(bin, "lyk"), []byte("#!/bin/sh\n: > \"$HOME/lyk-called\"\n"), 0700))
	script, err := filepath.Abs("testdata/ui-fake-claude.sh")
	must(t, err)
	cmd := exec.Command(script, "--version")
	cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, "claude"), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	must(t, err)
	if strings.TrimSpace(string(out)) != "2.1.0 (Claude Code)" {
		t.Fatalf("version output %q", out)
	}
	entries, err := os.ReadDir(home)
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("probe wrote run/session files: %v", entries)
	}
}

func TestUIFakeClaudeRunRecordsFailure(t *testing.T) {
	home := t.TempDir()
	must(t, os.WriteFile(filepath.Join(home, "lyk"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$HOME/result-args\"\n"), 0700))
	script, err := filepath.Abs("testdata/ui-fake-claude.sh")
	must(t, err)
	cmd := exec.Command(script, "--input-format", "stream-json")
	cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, "claude"), "PATH="+home+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader("{\"type\":\"user\"}\n")
	out, err := cmd.Output()
	must(t, err)
	dec := json.NewDecoder(strings.NewReader(string(out)))
	tools, text, result := 0, 0, false
	for {
		var frame struct {
			Type      string
			Message   struct{ Content []struct{ Type string } }
			SessionID string `json:"session_id"`
		}
		err := dec.Decode(&frame)
		if err == io.EOF {
			break
		}
		must(t, err)
		for _, c := range frame.Message.Content {
			if c.Type == "tool_use" {
				tools++
			}
			if c.Type == "text" {
				text++
			}
		}
		if frame.Type == "result" && frame.SessionID == "s" {
			result = true
		}
	}
	if tools != 2 || text != 3 || !result {
		t.Fatalf("frames: tools=%d text=%d result=%v", tools, text, result)
	}
	args, err := os.ReadFile(filepath.Join(home, "result-args"))
	must(t, err)
	if strings.TrimSpace(string(args)) != "result failed --reason the lab script stops here" {
		t.Fatalf("result command %q", args)
	}
	cwd, err := os.Getwd()
	must(t, err)
	_, err = os.Stat(filepath.Join(agent.ClaudeProjectDir(filepath.Join(home, "claude"), cwd), "s.jsonl"))
	must(t, err)
}

// uiSkillSourceTransport supplies only the browser lab's reserved published repo.
// All daemon, source and authentication traffic retains the ordinary transport.
type uiSkillSourceTransport struct {
	fallback         http.RoundTripper
	release, started chan struct{}
	startedOnce      *sync.Once
}

func (s uiSkillSourceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := ""
	target := r.URL.Host + r.URL.Path
	late := target == "api.github.com/repos/ui2-fixture/late" || strings.HasPrefix(target, "api.github.com/repos/ui2-fixture/late/") || strings.HasPrefix(target, "raw.githubusercontent.com/ui2-fixture/late/main/")
	if late {
		target = strings.Replace(target, "ui2-fixture/late", "ui2-fixture/published", 1)
	}
	if late && target == "raw.githubusercontent.com/ui2-fixture/published/main/SKILL.md" {
		s.startedOnce.Do(func() { close(s.started) })
		select {
		case <-s.release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	switch target {
	case "api.github.com/repos/ui2-fixture/published":
		body = `{"default_branch":"main"}`
	case "api.github.com/repos/ui2-fixture/published/git/trees/main":
		body = `{"tree":[{"path":"SKILL.md","type":"blob","size":120},{"path":"references/raw.bin","type":"blob","size":5},{"path":"scripts/check.py","type":"blob","size":24}]}`
	case "raw.githubusercontent.com/ui2-fixture/published/main/SKILL.md":
		body = "---\nname: published-review\ndescription: Published complete folder.\n---\n\n# Published review\n"
	case "raw.githubusercontent.com/ui2-fixture/published/main/references/raw.bin":
		body = string([]byte{0, 255, 128, 13, 10})
	case "raw.githubusercontent.com/ui2-fixture/published/main/scripts/check.py":
		body = "print('never executed')\n"
	default:
		fallback := s.fallback
		if fallback == nil {
			fallback = http.DefaultTransport
		}
		return fallback.RoundTrip(r)
	}
	if late {
		body = strings.ReplaceAll(body, "published-review", "late-review")
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
