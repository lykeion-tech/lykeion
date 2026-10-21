package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// jevStub answers score requests with the score its func gives each
// question's instructions, or with status when status is not 200.
type jevStub struct {
	mu       sync.Mutex
	status   int
	score    func(instructions string) float64
	requests []jevRequest
	maxState int // above this many bytes of state, answer as Jev does when a request is too long
}

type jevRequest struct {
	State     string
	Questions map[string]struct {
		Type         string
		Instructions string
		Criteria     []string
	}
}

func (l *lab) jev(t *testing.T, status int, score func(string) float64) *jevStub {
	t.Helper()
	s := &jevStub{status: status, score: score}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jevRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		if s.status != 200 {
			http.Error(w, "unavailable", s.status)
			return
		}
		if s.maxState > 0 && len(req.State) > s.maxState {
			http.Error(w, `{"error":{"type":"max_tokens_exceeded","message":"input exceeds the model's token limit"}}`, 400)
			return
		}
		answers := map[string]any{}
		for id, q := range req.Questions {
			answers[id] = map[string]any{"type": "score", "score": s.score(q.Instructions), "probabilities": map[string]float64{"4": 1}}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": answers})
	}))
	t.Cleanup(srv.Close)
	l.e.Jev = &jev.Client{BaseURL: srv.URL, Key: "k", Model: jev.Model}
	l.e.Retry = func() *jev.Retry {
		return &jev.Retry{Waits: []time.Duration{3 * time.Minute, 3 * time.Minute}, Max: 6 * time.Minute,
			Sleep: func(context.Context, time.Duration) error { return nil }}
	}
	return s
}

func (s *jevStub) calls() []jevRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// levels scores every criterion at level 5 except the ones named, which get
// the level given.
func levels(low map[string]float64) func(string) float64 {
	return func(instructions string) float64 {
		if l, ok := low[instructions]; ok {
			return l - 1
		}
		return 4
	}
}

func (l *lab) card(t *testing.T, id uuid.UUID) db.Card {
	t.Helper()
	var c db.Card
	l.in(t, func(tx *store.Tx) (err error) {
		c, err = tx.Q.GetCard(context.Background(), db.GetCardParams{WorkspaceID: l.ws, ID: id})
		return err
	})
	return c
}

// scoped creates a working study worked by agent from from to to.
func (l *lab) scoped(t *testing.T, from, to string, agent uuid.UUID) db.Card {
	t.Helper()
	typ := "agent"
	var c db.Card
	l.in(t, func(tx *store.Tx) (err error) {
		n, err := tx.Q.NextCardNumber(context.Background(), l.ws)
		if err != nil {
			return err
		}
		c, err = tx.Q.CreateCard(context.Background(), db.CreateCardParams{WorkspaceID: l.ws, Number: n, Title: "t", Question: "Why do cells age?",
			OwnerID: l.owner, FollowsFrom: []uuid.UUID{}, ScopeFrom: from, ScopeTo: to, AssigneeType: &typ, AssigneeID: &agent, BoardColumn: from, Round: 1})
		return err
	})
	return c
}

// done starts the study's current column if needed and has its leader or
// agent declare it done, citing keys, then hands the column to the auditor.
func (l *lab) done(t *testing.T, id uuid.UUID, cites ...string) {
	t.Helper()
	if err := l.declare(t, id, cites...); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Audit(context.Background(), l.ws, id); err != nil {
		t.Fatal(err)
	}
}

// declare has the column's leader or agent declare it done and returns
// Finish's answer.
func (l *lab) declare(t *testing.T, id uuid.UUID, cites ...string) error {
	t.Helper()
	ctx := context.Background()
	l.e.Kick(l.ws, id)
	c := l.card(t, id)
	return l.st.InWorkspace(ctx, l.ws, l.owner, func(tx *store.Tx) error {
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: l.ws, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return err
		}
		var run db.AgentRun
		for _, r := range runs {
			if r.Role == "leader" || r.Role == "agent" {
				run = r
			}
		}
		if run.ID == uuid.Nil {
			return fmt.Errorf("no leader or agent run in %s round %d", c.BoardColumn, c.Round)
		}
		_, err = l.e.Finish(ctx, tx, service.Writer{Card: id, AgentRun: run.ID, Column: c.BoardColumn, Role: run.Role, Round: c.Round},
			"The column is done; see the cited records.", cites)
		return err
	})
}

// settleAuditor completes the column's auditor run, as its daemon would, and
// lets the study move on.
func (l *lab) settleAuditor(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET status = 'completed' WHERE card_id = $1 AND role = 'auditor' AND status = 'queued'", id); err != nil {
		t.Fatal(err)
	}
	l.e.Kick(l.ws, id)
}

func (l *lab) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := l.env.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (l *lab) inbox(t *testing.T, id uuid.UUID, kind string) []string {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(), "SELECT text FROM inbox_item WHERE card_id = $1 AND kind = $2", id, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestVerdictThresholds(t *testing.T) {
	for _, tc := range []struct {
		levels []float64
		want   string
	}{
		{[]float64{4, 4, 5}, "high"},
		{[]float64{5, 2, 5}, "low"},
		{[]float64{3, 4, 5}, "middle"},
		{[]float64{4, 3.9, 5}, "middle"},
		{[]float64{2.0, 5, 5}, "low"},
	} {
		if got := Verdict(tc.levels); got != tc.want {
			t.Errorf("Verdict(%v) = %q, want %q", tc.levels, got, tc.want)
		}
	}
}

func TestHighMovesOnAndTheLastColumnEnds(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "hypotheses", a)
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" || got.State != "working" || got.Round <= c.Round {
		t.Fatalf("after a high score: %s %s round %d", got.BoardColumn, got.State, got.Round)
	}
	if runs := l.runs(t, c.ID); !slices.ContainsFunc(runs, func(r queued) bool { return r.Column == "hypotheses" && r.Role == "agent" }) {
		t.Fatalf("hypotheses did not start: %+v", runs)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'score' AND board_column = 'grounding'", c.ID); n != 3 {
		t.Fatalf("%d score records", n)
	}
	var verdict string
	l.env.Owner.QueryRow(context.Background(), "SELECT body->>'verdict' FROM record WHERE card_id = $1 AND kind = 'outcome'", c.ID).Scan(&verdict)
	if verdict != "high" {
		t.Fatalf("outcome %q", verdict)
	}
	l.done(t, c.ID)
	got = l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" || got.State != "done" {
		t.Fatalf("after the last column: %s %s", got.BoardColumn, got.State)
	}
	if items := l.inbox(t, c.ID, "done"); len(items) != 1 {
		t.Fatalf("done items: %v", items)
	}
	if items := l.inbox(t, c.ID, "middle_score"); len(items) != 0 {
		t.Fatalf("a high score raised a middle item: %v", items)
	}
}

func TestMiddleMovesOnWithAnInboxItem(t *testing.T) {
	l := newLab(t)
	const weak = "Does it answer what the researcher asked?"
	l.jev(t, 200, levels(map[string]float64{weak: 3, "Does it say what it couldn't do?": 3.5}))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "hypotheses", a)
	l.done(t, c.ID)
	if got := l.card(t, c.ID); got.BoardColumn != "hypotheses" || got.State != "working" {
		t.Fatalf("after a middle score: %s %s", got.BoardColumn, got.State)
	}
	items := l.inbox(t, c.ID, "middle_score")
	if len(items) != 1 || !strings.Contains(items[0], weak) {
		t.Fatalf("middle items: %v", items)
	}
}

func TestLowRedoesOnceThenBlocks(t *testing.T) {
	l := newLab(t)
	const weak = "Does it say what it couldn't do?"
	l.jev(t, 200, levels(map[string]float64{weak: 2}))
	c := l.study(t, "hypotheses", true, nil)
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" || got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after the first low score: %s %s redos %d round %d", got.BoardColumn, got.State, got.Redos, got.Round)
	}
	var redo *queued
	for _, r := range l.runs(t, c.ID) {
		if r.Role == "leader" && r.Scope == ContinuityScope(c.ID, "leader", "hypotheses", c.Round+1) {
			redo = &r
		}
	}
	if redo == nil || !strings.Contains(redo.Prompt, weak) {
		t.Fatalf("the redo's leader run lacks the weak criterion: %+v", l.runs(t, c.ID))
	}
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	got = l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "low_score" || got.BoardColumn != "hypotheses" {
		t.Fatalf("after the second low score: %s %s %s", got.BoardColumn, got.State, got.BlockKind)
	}
	if items := l.inbox(t, c.ID, "blocked"); len(items) != 1 {
		t.Fatalf("blocked items: %v", items)
	}
	l.e.Kick(l.ws, c.ID)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND round > $2", c.ID, got.Round); n != 0 {
		t.Fatalf("a blocked study started %d runs", n)
	}
	// The owner's comment redoes the column afresh: its leader resumes no
	// earlier attempt's session.
	l.e.OnComment(l.ws, c.ID, l.owner, "Say what the data could not show.")
	scopes := map[string]int{}
	for _, r := range l.runs(t, c.ID) {
		if r.Role == "leader" {
			scopes[r.Scope]++
		}
	}
	if len(scopes) != 3 {
		t.Fatalf("three attempts' leaders share sessions: %v", scopes)
	}
}

func TestScoringWithoutJevNeverMovesTheStudy(t *testing.T) {
	for _, tc := range []string{"503", "no key"} {
		t.Run(tc, func(t *testing.T) {
			l := newLab(t)
			stub := l.jev(t, 503, nil)
			if tc == "no key" {
				l.e.Jev = nil
			}
			var warned atomic.Int32
			prev := slog.Default()
			slog.SetDefault(slog.New(countHandler{msg: "scoring unavailable", n: &warned}))
			t.Cleanup(func() { slog.SetDefault(prev) })
			a := l.agent(t, "Curie", "claude", "", "You are Curie.")
			c := l.scoped(t, "grounding", "hypotheses", a)
			l.done(t, c.ID)
			l.e.Kick(l.ws, c.ID) // the next tick retries
			got := l.card(t, c.ID)
			if got.BoardColumn != "grounding" || got.State != "working" || got.Round != c.Round || got.Redos != 0 {
				t.Fatalf("the study moved: %s %s round %d redos %d", got.BoardColumn, got.State, got.Round, got.Redos)
			}
			if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind IN ('score', 'outcome')", c.ID); n != 0 {
				t.Fatalf("%d score or outcome records without Jev", n)
			}
			if items := l.inbox(t, c.ID, "scoring_unavailable"); len(items) != 1 {
				t.Fatalf("scoring_unavailable items: %v", items)
			}
			if tc == "503" && len(stub.calls()) != 6 {
				t.Fatalf("Jev asked %d times over two passes, want 3 attempts each", len(stub.calls()))
			}
			if tc == "no key" && warned.Load() != 1 {
				t.Fatalf("with no key, %d passes tried to score after the owner heard", warned.Load())
			}
		})
	}
}

// countHandler counts the log lines with one message.
type countHandler struct {
	msg string
	n   *atomic.Int32
}

func (h countHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h countHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.msg {
		h.n.Add(1)
	}
	return nil
}
func (h countHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h countHandler) WithGroup(string) slog.Handler      { return h }

func TestAuditorRunsLikeEveryRun(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	c := l.study(t, "hypotheses", true, nil)
	l.done(t, c.ID)
	var role, harness, prompt, scope string
	var runJSON []byte
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT role, harness, prompt, continuity_scope, row_to_json(agent_run) FROM agent_run WHERE card_id = $1 AND role = 'auditor'", c.ID).
		Scan(&role, &harness, &prompt, &scope, &runJSON); err != nil {
		t.Fatalf("no auditor run: %v", err)
	}
	if scope != ContinuityScope(c.ID, "auditor", c.BoardColumn, c.Round) || strings.Contains(string(runJSON), `"run_private"`) || harness != "codex" || !strings.Contains(prompt, "The column is done; see the cited records.") {
		t.Fatalf("auditor run: scope %q harness %s prompt:\n%s JSON %s", scope, harness, prompt, runJSON)
	}
	if n := len(stub.calls()); n != 0 {
		t.Fatalf("Jev scored before the auditor settled: %d calls", n)
	}
	l.e.Kick(l.ws, c.ID)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'auditor'", c.ID); n != 1 {
		t.Fatalf("%d auditor runs", n)
	}
	const finding = "Claim 2 quotes a passage no call holds."
	var run uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT id FROM agent_run WHERE card_id = $1 AND role = 'auditor'", c.ID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: run, Column: "hypotheses", Role: "auditor", Round: c.Round},
			"audit", map[string]any{"text": "One problem.", "findings": []string{finding}})
		return err
	})
	l.settleAuditor(t, c.ID)
	if calls := stub.calls(); len(calls) != 1 || !strings.Contains(calls[0].State, finding) {
		t.Fatalf("Jev after the auditor: %d calls, or the audit's findings missing from the state", len(calls))
	}

	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	solo := l.scoped(t, "grounding", "grounding", a)
	l.done(t, solo.ID)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'auditor'", solo.ID); n != 0 {
		t.Fatalf("%d auditor runs on a column without a Team", n)
	}
	if got := l.card(t, solo.ID); got.State != "done" {
		t.Fatalf("the no-Team column was not scored: %s", got.State)
	}
}

func TestTheCoreCriteriaAreAskedVerbatim(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	m := l.man
	m.Criteria = []string{"Does it name the cell line?"}
	b, _ := json.Marshal(m)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE field_pack SET manifest = $1 WHERE id = $2", b, l.pack.ID); err != nil {
		t.Fatal(err)
	}
	c := l.study(t, "hypotheses", true, nil)
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("%d Jev calls", len(calls))
	}
	ids := []string{}
	for id := range calls[0].Questions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var asked []string
	for _, id := range ids {
		q := calls[0].Questions[id]
		if q.Type != "score" || !slices.Equal(q.Criteria, []string{"Not at all", "Barely", "Partly", "Mostly", "Fully"}) {
			t.Fatalf("question %s: %+v", id, q)
		}
		asked = append(asked, q.Instructions)
	}
	want := []string{"Does the work cite where its facts came from?", "Does it answer what the researcher asked?", "Does it say what it couldn't do?", "Does it name the cell line?"}
	if !slices.Equal(asked, want) {
		t.Fatalf("asked %q", asked)
	}
	var core, team int
	rows, _ := l.env.Owner.Query(context.Background(), "SELECT body->>'source', (body->>'level')::float FROM record WHERE card_id = $1 AND kind = 'score'", c.ID)
	for rows.Next() {
		var src string
		var level float64
		rows.Scan(&src, &level)
		if level != 5 {
			t.Fatalf("level %v for a Jev score of 4", level)
		}
		switch src {
		case "core":
			core++
		case "team":
			team++
		}
	}
	rows.Close()
	if core != 3 || team != 1 {
		t.Fatalf("score records: %d core, %d team", core, team)
	}
}

func TestTheStateFitsJevsLimit(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	l.e.Kick(l.ws, c.ID)
	var cites []string
	body, _ := json.Marshal(map[string]string{"text": strings.Repeat("x", 4<<10)})
	l.in(t, func(tx *store.Tx) error {
		var run db.AgentRun
		runs, err := tx.Q.RoundAgentRuns(context.Background(), db.RoundAgentRunsParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding", Round: c.Round})
		if err != nil || len(runs) == 0 {
			return fmt.Errorf("no agent run: %v", err)
		}
		run = runs[0]
		for range 200 {
			rec, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: run.ID, Column: "grounding", Role: "agent", Round: c.Round}, "note", json.RawMessage(body))
			if err != nil {
				return err
			}
			cites = append(cites, rec.Key)
		}
		return nil
	})
	l.done(t, c.ID, cites...)
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("%d Jev calls", len(calls))
	}
	q, _ := json.Marshal(calls[0].Questions)
	if n := len(calls[0].State) + len(q); n > 128<<10 {
		t.Fatalf("state plus questions is %d bytes", n)
	}
	if !strings.Contains(calls[0].State, "[cut:") {
		t.Fatal("no cut mark in the state")
	}
	if !strings.Contains(calls[0].State, "Record r200") {
		t.Fatal("the cut dropped a record instead of shortening it")
	}
}

func TestALongQuestionStillFitsJevsLimit(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET question = $1 WHERE id = $2", strings.Repeat("Why? ", 40<<10), c.ID); err != nil {
		t.Fatal(err)
	}
	l.done(t, c.ID)
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("%d Jev calls", len(calls))
	}
	q, _ := json.Marshal(calls[0].Questions)
	if n := len(calls[0].State) + len(q); n > 128<<10 {
		t.Fatalf("state plus questions is %d bytes", n)
	}
	if !strings.Contains(calls[0].State, cutMark) {
		t.Fatal("no cut mark in the state")
	}
}

func TestALongSummaryIsCutInsideItsFence(t *testing.T) {
	s := evidence(strings.Repeat("s", 200<<10), nil, 32<<10)
	if len(s) > 32<<10 || !strings.Contains(s, cutMark) || !strings.Contains(s, "-END\n") {
		t.Fatalf("%d bytes, cut mark %v, fence closed %v", len(s), strings.Contains(s, cutMark), strings.Contains(s, "-END\n"))
	}
}

// The dashboard reads these sentences back, so their shape is pinned.
func TestInboxSentences(t *testing.T) {
	weak := weakCriterion("Does it say what it couldn't do?", 3)
	if weak != "“Does it say what it couldn't do?” (level 3 of 5)" {
		t.Fatalf("weak = %q", weak)
	}
	for got, want := range map[string]string{
		middleScoreText(7, "grounding", weak, false): "Study #7's Grounding column scored in the middle; its weakest criterion was “Does it say what it couldn't do?” (level 3 of 5).",
		middleScoreText(7, "grounding", weak, true):  "Study #7's Grounding column scored in the middle and the study finished; its weakest criterion was “Does it say what it couldn't do?” (level 3 of 5).",
		blockedText(7, "grounding", weak):            "Study #7 scored low twice in its Grounding column, lowest on “Does it say what it couldn't do?” (level 3 of 5). Comment on the study to tell the Team what to fix.",
		doneText(7, "reporting"):                     "Study #7 is done, in its Reporting column.",
		unavailableText(7, "grounding"):              "Scoring is unavailable for study #7's Grounding column. It stays there, and Lykeion keeps retrying.",
	} {
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
}

func TestACallWhoseBlobIsGoneIsScoredAsUnreadable(t *testing.T) {
	l := newLab(t)
	l.e.Blobs = storage.Local{Dir: t.TempDir()}
	stub := l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	l.e.Kick(l.ws, c.ID)
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		if _, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding", Round: c.Round,
			Kind: "call", Role: "agent", Key: "c1", Body: []byte(`{"answer":"the kept start"}`), BlobKey: l.ws.String() + "/" + c.ID.String() + "/gone"}); err != nil {
			return err
		}
		_, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding", Round: c.Round,
			Kind: "claim", Role: "agent", Key: "r1", Body: []byte(`{"cites":["call:c1"],"quote":"a passage past the cut"}`)})
		return err
	})
	l.done(t, c.ID, "r1")
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("a missing blob kept the column from being scored: %s", got.State)
	}
	if items := l.inbox(t, c.ID, "scoring_unavailable"); len(items) != 0 {
		t.Fatalf("a missing blob was reported as a Jev outage: %v", items)
	}
	if calls := stub.calls(); len(calls) != 1 || !strings.Contains(calls[0].State, "r1: could not be checked: call c1's full answer is unreadable") {
		t.Fatalf("Jev was not told the call is unreadable: %d calls", len(calls))
	}
}

func TestAnAuditorCopyEditedToMatchAMemberIsNotRun(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE agent SET harness = m.harness, model = m.model
		FROM agent m WHERE m.id = $2 AND agent.id = $1`, l.team["auditor"], l.team["member-1"]); err != nil {
		t.Fatal(err)
	}
	c := l.study(t, "hypotheses", true, nil)
	l.done(t, c.ID)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'auditor'", c.ID); n != 0 {
		t.Fatalf("an auditor sharing a member's harness and model ran: %d runs", n)
	}
	calls := stub.calls()
	if len(calls) != 1 || !strings.Contains(calls[0].State, "the researcher's copy of the auditor runs the same harness and model as ") {
		t.Fatalf("Jev was not told why there is no audit: %d calls", len(calls))
	}
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("the column was not scored: %s", got.State)
	}
}

func TestScoringStateReportsValues(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "experimentation", "experimentation", a)
	l.e.Kick(l.ws, c.ID)
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		if _, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "experimentation", Round: c.Round,
			Kind: "run", Role: "server", Key: "x1", Body: []byte(`{"tool":"run_script","output":"effect 0.123456\n","exit_status":0}`)}); err != nil {
			return err
		}
		_, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "experimentation", Round: c.Round,
			Kind: "result", Role: "agent", Key: "r1", Body: []byte(`{"cites":["run:x1"],"value":0.12}`)})
		return err
	})
	l.done(t, c.ID, "r1")
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("%d Jev calls", len(calls))
	}
	s := calls[0].State
	for _, want := range []string{"Value check, done by Lykeion's code:", `- r1: found in script run x1: "0.12"`,
		"Judge whether each item's kind fits its content, and whether its results sit in value fields."} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in the state:\n%s", want, s)
		}
	}
}
