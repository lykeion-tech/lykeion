package round

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type fix struct {
	env   *testutil.Env
	e     *Engine
	ws    uuid.UUID
	owner uuid.UUID
}

func setup(t *testing.T) *fix {
	t.Helper()
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	blobs := storage.Local{Dir: t.TempDir()}
	f := &fix{env: env, ws: uuid.New()}
	f.e = &Engine{Store: st, AgentRuns: &service.AgentRuns{Store: st}, Blobs: blobs, Now: time.Now, Inline: true}
	ctx := context.Background()
	u, _ := st.Global().UpsertUserByEmail(ctx, "a@lab.org")
	f.owner = u.ID
	env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'w', 'w')`, f.ws)
	return f
}

func (f *fix) card(t *testing.T, column string, round int32) uuid.UUID {
	var id uuid.UUID
	f.env.Owner.QueryRow(context.Background(), `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, round)
		VALUES ($1, (SELECT count(*) + 1 FROM card), 't', 'Does X raise Y?', $2, $3, $4) RETURNING id`, f.ws, f.owner, column, round).Scan(&id)
	return id
}

func (f *fix) agentRuns(t *testing.T, card uuid.UUID) []db.AgentRun {
	var out []db.AgentRun
	f.e.Store.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		c, _ := tx.Q.GetCard(context.Background(), db.GetCardParams{WorkspaceID: f.ws, ID: card})
		out, _ = tx.Q.RoundAgentRuns(context.Background(), db.RoundAgentRunsParams{WorkspaceID: f.ws, CardID: card, BoardColumn: c.BoardColumn, Round: c.Round})
		return nil
	})
	return out
}

func (f *fix) cardRow(t *testing.T, card uuid.UUID) db.Card {
	var c db.Card
	f.e.Store.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		c, _ = tx.Q.GetCard(context.Background(), db.GetCardParams{WorkspaceID: f.ws, ID: card})
		return nil
	})
	return c
}

// settle marks an agent run completed directly, standing in for a daemon,
// without any record from the role.
func (f *fix) settle(t *testing.T, id uuid.UUID) {
	f.env.Owner.Exec(context.Background(), `UPDATE agent_run SET status = 'completed', completed_at = now() WHERE id = $1`, id)
}

// result records a result kind for an agent run, as its role would through lyk.
func (f *fix) result(t *testing.T, id uuid.UUID, kind, reason string) {
	f.env.Owner.Exec(context.Background(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, agent_run_id, body)
		SELECT workspace_id, card_id, board_column, round, $2, role, id, jsonb_build_object('reason', $3::text) FROM agent_run WHERE id = $1`, id, kind, reason)
}

// complete is an agent run that searched and found nothing: a legitimate result.
func (f *fix) complete(t *testing.T, id uuid.UUID) {
	f.result(t, id, "nothing_found", "nothing")
	f.settle(t, id)
}

func (f *fix) inbox(t *testing.T, card uuid.UUID) []string {
	var out []string
	rows, _ := f.env.Owner.Query(context.Background(), "SELECT text FROM inbox_item WHERE card_id = $1 AND kind = 'escalated'", card)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestWIPPullRespectsWIPAndQueuesTheScoper(t *testing.T) {
	f := setup(t)
	f.card(t, board.Grounding, 1)
	a := f.card(t, board.Backlog, 0)
	b := f.card(t, board.Backlog, 0)
	if err := f.e.WIPPull(context.Background(), f.ws); err != nil {
		t.Fatal(err)
	}
	if f.cardRow(t, a).BoardColumn != board.Grounding || f.cardRow(t, b).BoardColumn != board.Backlog {
		t.Fatal("WIP limit 2 not respected")
	}
	f.e.Advance(context.Background(), f.ws, a)
	ts := f.agentRuns(t, a)
	if len(ts) != 1 || ts[0].Role != ground.Scoper || ts[0].Round != 1 || ts[0].Skill == "" || ts[0].ResponsibleUserID != f.owner ||
		ts[0].ContinuityScope != "card:"+a.String()+"/role:scoper" || ts[0].RunPrivate {
		t.Fatalf("%+v", ts)
	}
	if !strings.Contains(ts[0].Prompt, "Search and fetch only through lyk_sources") {
		t.Fatalf("prompt:\n%s", ts[0].Prompt)
	}
}

func TestDoersKeepTheirScopeAndJudgesGetTheRound(t *testing.T) {
	card := uuid.New()
	if s := ContinuityScope(card, ground.Literature, 3, false); s != "card:"+card.String()+"/role:literature-scout" {
		t.Fatalf("doer: %s", s)
	}
	if s := ContinuityScope(card, ground.Reviewer, 3, true); s != "card:"+card.String()+"/role:grounding-reviewer/round:3" {
		t.Fatalf("judge: %s", s)
	}
	// The daemon refuses any other shape.
	shape := regexp.MustCompile(`^[a-z]+:[0-9a-f-]{36}(/[a-z]+:[A-Za-z0-9_.-]{1,64})*$`)
	for _, s := range []string{ContinuityScope(card, ground.Scoper, 1, false), ContinuityScope(card, ground.Reviewer, 12, true)} {
		if !shape.MatchString(s) {
			t.Fatalf("scope %q does not match the daemon's pattern", s)
		}
	}
}

func TestPhasesQueueOnceEvenWhenAgentRunsFinishTogether(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(context.Background(), f.ws, card)
	f.complete(t, f.agentRuns(t, card)[0].ID)
	f.e.Advance(context.Background(), f.ws, card)
	scouts := f.agentRuns(t, card)[1:]
	if len(scouts) != 4 {
		t.Fatalf("want 4 scouts, got %d", len(scouts))
	}
	for _, s := range scouts {
		f.complete(t, s.ID)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.e.Advance(context.Background(), f.ws, card) }()
	}
	wg.Wait()
	// Every scout found nothing, the data scout included, so the gate decides
	// cannot_proceed before any reviewer: what matters here is that concurrent
	// advances decided one gate.
	var gates int
	f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'gate'", card).Scan(&gates)
	c := f.cardRow(t, card)
	if gates != 1 || c.Ending != "stopped" || c.EndReason != "no usable data: nothing" {
		t.Fatalf("gates %d, %+v", gates, c)
	}
}

func TestFailedRoleAgentRunEscalatesWithItsReason(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(context.Background(), f.ws, card)
	f.env.Owner.Exec(context.Background(), `UPDATE agent_run SET status = 'failed', error = 'prompt too long', failure_reason = 'context_exhausted' WHERE card_id = $1`, card)
	f.e.Advance(context.Background(), f.ws, card)
	c := f.cardRow(t, card)
	if c.Flag != board.Escalated || c.Rerun != ground.Scoper || !strings.Contains(c.HandoffNote, "context_exhausted") {
		t.Fatalf("%+v", c)
	}
	if items := f.inbox(t, card); len(items) != 1 || !strings.Contains(items[0], "failed (context_exhausted): prompt too long") {
		t.Fatalf("inbox %v", items)
	}
}

func TestCompletedWithoutAResultEscalates(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(context.Background(), f.ws, card)
	f.settle(t, f.agentRuns(t, card)[0].ID)
	f.e.Advance(context.Background(), f.ws, card)
	f.e.Advance(context.Background(), f.ws, card)
	c := f.cardRow(t, card)
	items := f.inbox(t, card)
	if c.Flag != board.Escalated || len(items) != 1 || !strings.Contains(items[0], "without recording a result (agent_error)") {
		t.Fatalf("%+v %v", c, items)
	}
	var runs int
	var spent float64
	f.env.Owner.QueryRow(context.Background(), "SELECT count(*), (SELECT spent_usd FROM card WHERE id = $1) FROM agent_run WHERE card_id = $1", card).Scan(&runs, &spent)
	if runs != 1 || spent != 0 {
		t.Fatalf("an escalation queued work or spent money: agent runs %d, spent %v", runs, spent)
	}
}

func TestFailedResultEscalates(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(context.Background(), f.ws, card)
	id := f.agentRuns(t, card)[0].ID
	f.result(t, id, "failed", "the question names no outcome I can scope")
	f.settle(t, id)
	f.e.Advance(context.Background(), f.ws, card)
	if items := f.inbox(t, card); len(items) != 1 || !strings.Contains(items[0], "the question names no outcome") {
		t.Fatalf("%v", items)
	}
}

func TestMissingSkillQueuesNothingAndEscalates(t *testing.T) {
	f := setup(t)
	StageRoles[board.Hypotheses] = Roles{Phases: [][]string{{"no-such-role"}}, Reviewer: "no-such-reviewer",
		Kinds: ground.Kinds, Check: ground.Check, Finalize: ground.Finalize, RoleKinds: ground.RoleKinds}
	t.Cleanup(func() { delete(StageRoles, board.Hypotheses) })
	card := f.card(t, board.Hypotheses, 1)
	f.e.Advance(context.Background(), f.ws, card)
	if n := len(f.agentRuns(t, card)); n != 0 {
		t.Fatalf("queued %d agent runs without a skill", n)
	}
	c := f.cardRow(t, card)
	if items := f.inbox(t, card); c.Flag != board.Escalated || len(items) != 1 || !strings.Contains(items[0], "no skill for the no-such-role role") {
		t.Fatalf("%+v %v", c, items)
	}
	if c.Rerun != "" {
		t.Fatalf("rerun %q: resuming would skip the phase's other roles", c.Rerun)
	}
}

func TestHandoffNoteReachesTheRedoneRole(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 2)
	f.env.Owner.Exec(context.Background(), `UPDATE card SET rerun = 'contrarian', handoff_note = 'search.contrary: the contrary search never ran' WHERE id = $1`, card)
	f.e.Advance(context.Background(), f.ws, card)
	ts := f.agentRuns(t, card)
	if len(ts) != 1 || ts[0].Role != ground.Contrarian || ts[0].HandoffNote != "search.contrary: the contrary search never ran" ||
		!strings.Contains(ts[0].Prompt, "the contrary search never ran") || !strings.Contains(ts[0].Prompt, "no longer count") ||
		ts[0].ContinuityScope != "card:"+card.String()+"/role:contrarian" {
		t.Fatalf("%+v", ts)
	}
}

func TestStaleRoundResultsCannotMoveTheCard(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(context.Background(), f.ws, card)
	old := f.agentRuns(t, card)[0]
	// a person sends the card back to Grounding while round 1 is running:
	// the card is now in round 2, and round 1's agent run finishing must not count
	f.env.Owner.Exec(context.Background(), `UPDATE card SET round = 2 WHERE id = $1`, card)
	f.complete(t, old.ID)
	f.e.Advance(context.Background(), f.ws, card)
	ts := f.agentRuns(t, card)
	if len(ts) != 1 || ts[0].Round != 2 || ts[0].Role != ground.Scoper {
		t.Fatalf("round 2 should start from its own scoper: %+v", ts)
	}
}

func TestOutOfBudgetQueuesNothing(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.env.Owner.Exec(context.Background(), `UPDATE card SET budget_usd = 1, spent_usd = 1 WHERE id = $1`, card)
	f.e.Advance(context.Background(), f.ws, card)
	if c := f.cardRow(t, card); c.Flag != board.OutOfBudget || len(f.agentRuns(t, card)) != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestOutOfBudgetMarksQueuedAgentRuns(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(context.Background(), f.ws, card)
	f.env.Owner.Exec(context.Background(), `UPDATE card SET budget_usd = 1, spent_usd = 1 WHERE id = $1`, card)
	f.e.Advance(context.Background(), f.ws, card)
	ts := f.agentRuns(t, card)
	if len(ts) != 1 || ts[0].QueueReason == nil || *ts[0].QueueReason != "budget" {
		t.Fatalf("%+v", ts)
	}
}

func TestTickMarksAgentRunsOfAnOfflineOwner(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(context.Background(), f.ws, card)
	if err := f.e.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ts := f.agentRuns(t, card); ts[0].QueueReason == nil || *ts[0].QueueReason != "runtime_offline" {
		t.Fatalf("%+v", ts[0])
	}
}

func TestResultKindsAreOpenToEveryRole(t *testing.T) {
	k := Kinds()
	for _, role := range []string{ground.Scoper, ground.DataScout, ground.Reviewer} {
		for _, name := range ResultKinds {
			if !slices.Contains(k[name].Roles, role) {
				t.Fatalf("%s may not write %s", role, name)
			}
		}
	}
	if k["nothing_found"].Validate(json.RawMessage(`{"reason":" "}`)) == nil {
		t.Fatal("a result without a reason was accepted")
	}
}

func TestSettingsDefaults(t *testing.T) {
	s := ParseSettings(json.RawMessage(`{"harness":{"roles":{"grounding-reviewer":"codex"}}}`))
	if s.WIPLimit != 2 || s.Limits.ConsecutiveFails != 3 || s.HarnessFor("grounding-reviewer") != "codex" || s.HarnessFor("scoper") != "claude" || s.Jev.Enabled {
		t.Fatalf("%+v", s)
	}
	// A model stored in the settings by any route is ignored: the pin holds.
	pinned := ParseSettings(json.RawMessage(`{"jev":{"enabled":true,"model":"jev-1.14.0"}}`))
	if c := (&Engine{JevKey: "k"}).jev(pinned); c == nil || c.Model != jev.Model {
		t.Fatalf("jev client %+v", c)
	}
}

// atGate brings a Grounding card to its gate: the scoper and the four scouts
// have run and found nothing, and nothing has advanced the card since.
func (f *fix) atGate(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	card := f.card(t, board.Grounding, 1)
	f.e.Advance(ctx, f.ws, card)
	f.complete(t, f.agentRuns(t, card)[0].ID)
	f.e.Advance(ctx, f.ws, card)
	for _, s := range f.agentRuns(t, card)[1:] {
		f.complete(t, s.ID)
	}
	return card
}

// slowJev turns Jev egress on and swaps Grounding's check for one that asks
// a fake Jev, which holds every request until release is closed, then leaves
// a draft for the reviewer. entered receives once per request; checks counts
// the gate's runs.
func (f *fix) slowJev(t *testing.T) (entered chan struct{}, release chan struct{}, checks *atomic.Int32) {
	t.Helper()
	entered, release, checks = make(chan struct{}, 16), make(chan struct{}), new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { // runs before srv.Close, which waits for held requests
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f.e.JevKey, f.e.JevURL = "k", srv.URL
	f.env.Owner.Exec(context.Background(), `UPDATE workspace SET settings = '{"jev":{"enabled":true}}' WHERE id = $1`, f.ws)
	prev := StageRoles[board.Grounding]
	roles := prev
	roles.Check = func(ctx context.Context, v *records.View, j *jev.Client) (*board.Gate, error) {
		checks.Add(1)
		if j == nil {
			t.Error("the gate got no Jev client")
		} else {
			j.Choice(ctx, "state", "question", map[string]string{"yes": "yes", "no": "no"})
		}
		return &board.Gate{Column: board.Grounding, InputDigest: v.Digest(ground.ReviewKinds...)}, nil
	}
	StageRoles[board.Grounding] = roles
	t.Cleanup(func() { StageRoles[board.Grounding] = prev })
	return entered, release, checks
}

func (f *fix) count(t *testing.T, card uuid.UUID, sql string) int {
	var n int
	f.env.Owner.QueryRow(context.Background(), sql, card).Scan(&n)
	return n
}

func TestSettleIsNotHeldBehindAGateAskingJev(t *testing.T) {
	f := setup(t)
	card := f.atGate(t)
	entered, release, _ := f.slowJev(t)
	done := make(chan error, 1)
	go func() { done <- f.e.Advance(context.Background(), f.ws, card) }()
	<-entered
	// what F2's settle does to the card row when one of its agent runs finishes
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		return tx.Q.AddCardSpend(ctx, db.AddCardSpendParams{Usd: 0.5, ID: card, WorkspaceID: f.ws})
	})
	close(release)
	if err != nil {
		t.Fatalf("a settlement waited on the gate's Jev call: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// a settlement changes spend, not what the gate judged: the gate stands
	c := f.cardRow(t, card)
	if c.State != board.InReview || c.GatingUntil != nil ||
		f.count(t, card, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'grounding-reviewer'") != 1 {
		t.Fatalf("%+v", c)
	}
	// the reviewer is a judge: its scope carries the round it judges
	if f.count(t, card, `SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'grounding-reviewer' AND NOT run_private
		AND continuity_scope = 'card:' || $1::text || '/role:grounding-reviewer/round:' || round`) != 1 {
		t.Fatal("the reviewer's continuity scope has no round")
	}
}

func TestRecordsChangedDuringTheGateAreJudgedAfresh(t *testing.T) {
	f := setup(t)
	card := f.atGate(t)
	entered, release, checks := f.slowJev(t)
	done := make(chan error, 1)
	go func() { done <- f.e.Advance(context.Background(), f.ws, card) }()
	<-entered
	// a record lands while Jev is asked: the gate no longer judges what is there
	f.result(t, f.agentRuns(t, card)[1].ID, "nothing_found", "searched again")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	drafts := f.count(t, card, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'gate_draft'")
	if checks.Load() != 2 || drafts != 1 {
		t.Fatalf("checks %d, drafts %d: the stale gate must be dropped and the card judged again", checks.Load(), drafts)
	}
}

func TestConcurrentAdvancesAskJevOnce(t *testing.T) {
	f := setup(t)
	card := f.atGate(t)
	entered, release, checks := f.slowJev(t)
	done := make(chan error, 1)
	go func() { done <- f.e.Advance(context.Background(), f.ws, card) }()
	<-entered
	// the card is leased: a second pass leaves it alone instead of asking again
	if err := f.e.Advance(context.Background(), f.ws, card); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := checks.Load(); n != 1 || f.count(t, card, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'gate_draft'") != 1 {
		t.Fatalf("the gate ran %d times", n)
	}
}

func TestAnExpiredGatingLeaseIsTakenOver(t *testing.T) {
	f := setup(t)
	card := f.atGate(t)
	// a node died mid-call an hour ago
	f.env.Owner.Exec(context.Background(), `UPDATE card SET gating_until = now() - interval '1 hour' WHERE id = $1`, card)
	f.e.Advance(context.Background(), f.ws, card)
	if c := f.cardRow(t, card); c.Ending != "stopped" || c.GatingUntil != nil {
		t.Fatalf("%+v", c)
	}
}

var _ = sources.Hosted{}

func TestEngineSendsPinnedJevModelDespiteStoredOverride(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		sent = body.Model
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"choice":"yes","probabilities":{"yes":0.9,"no":0.1},"confidence":0.9}}}`))
	}))
	defer srv.Close()
	set := ParseSettings(json.RawMessage(`{"jev":{"enabled":true,"model":"jev-1.14.0"}}`))
	client := (&Engine{JevKey: "k", JevURL: srv.URL}).jev(set)
	if client == nil {
		t.Fatal("enabled engine returned no Jev client")
	}
	if _, err := client.Choice(context.Background(), "state", "question", map[string]string{"yes": "yes", "no": "no"}); err != nil {
		t.Fatal(err)
	}
	if sent != "jev-1.13.0" {
		t.Fatalf("sent model %q", sent)
	}
}

func TestWIPPullSkipsExhaustedBacklogCardsAndNotifiesOwner(t *testing.T) {
	f := setup(t)
	f.card(t, board.Grounding, 1)
	exhausted := f.card(t, board.Backlog, 0)
	eligible := f.card(t, board.Backlog, 0)
	if _, err := f.env.Owner.Exec(context.Background(), `UPDATE card SET budget_usd = 1, spent_usd = 1 WHERE id = $1`, exhausted); err != nil {
		t.Fatal(err)
	}
	if err := f.e.WIPPull(context.Background(), f.ws); err != nil {
		t.Fatal(err)
	}
	c := f.cardRow(t, exhausted)
	if c.BoardColumn != board.Backlog || c.Flag != board.OutOfBudget || c.Round != 0 || len(f.agentRuns(t, exhausted)) != 0 {
		t.Fatalf("exhausted card started: %+v", c)
	}
	if c := f.cardRow(t, eligible); c.BoardColumn != board.Grounding || c.Round != 1 {
		t.Fatalf("eligible card did not start: %+v", c)
	}
	var notices, started int
	if err := f.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM inbox_item WHERE card_id = $1 AND user_id = $2 AND kind = 'out_of_budget'`, exhausted, f.owner).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if err := f.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'card.started'`, exhausted).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if notices != 1 || started != 0 {
		t.Fatalf("notices %d, started events %d", notices, started)
	}
	if err := f.e.WIPPull(context.Background(), f.ws); err != nil {
		t.Fatal(err)
	}
	if err := f.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'out_of_budget'`, exhausted).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 1 {
		t.Fatalf("repeat pull duplicated notice: %d", notices)
	}
}

func TestSettledScoutFailureEscalatesWhilePeersAreInFlight(t *testing.T) {
	for _, tc := range []struct{ name, status, reason, result, want string }{
		{"agent error", "failed", "agent_error", "", "failed (agent_error)"},
		{"context exhausted", "failed", "context_exhausted", "", "failed (context_exhausted)"},
		{"cancelled", "cancelled", "", "", "run cancelled"},
		{"failed result", "completed", "", "failed", "recorded that it failed: no answer"},
		{"no result", "completed", "", "", "without recording a result (agent_error)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			card := f.card(t, board.Grounding, 1)
			if err := f.e.Advance(ctx, f.ws, card); err != nil {
				t.Fatal(err)
			}
			f.complete(t, f.agentRuns(t, card)[0].ID)
			if err := f.e.Advance(ctx, f.ws, card); err != nil {
				t.Fatal(err)
			}
			var failed db.AgentRun
			for _, run := range f.agentRuns(t, card) {
				if run.Role == ground.Literature {
					failed = run
				}
			}
			if failed.ID == uuid.Nil {
				t.Fatal("literature scout not queued")
			}
			if tc.result != "" {
				f.result(t, failed.ID, tc.result, "no answer")
			}
			if _, err := f.env.Owner.Exec(ctx, `UPDATE agent_run SET status = $2, failure_reason = NULLIF($3, '') WHERE id = $1`, failed.ID, tc.status, tc.reason); err != nil {
				t.Fatal(err)
			}
			if err := f.e.Advance(ctx, f.ws, card); err != nil {
				t.Fatal(err)
			}
			c := f.cardRow(t, card)
			items := f.inbox(t, card)
			if c.Flag != board.Escalated || c.Rerun != ground.Literature || c.Round != 2 || len(items) != 1 || !strings.Contains(items[0], tc.want) {
				t.Fatalf("did not escalate before peers finished: %+v, inbox %v", c, items)
			}
			var peers int
			if err := f.env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run WHERE card_id = $1 AND round = 1 AND role <> 'literature-scout' AND status = 'queued'`, card).Scan(&peers); err != nil {
				t.Fatal(err)
			}
			if peers != 3 {
				t.Fatalf("peer runs changed: %d queued", peers)
			}
			if runs := f.agentRuns(t, card); len(runs) != 0 {
				t.Fatalf("escalation queued more work: %+v", runs)
			}
		})
	}
}
