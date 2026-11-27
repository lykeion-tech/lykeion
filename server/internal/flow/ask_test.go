package flow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// askBy records run's ask hand-in, with a scope decision from member
// (none when empty), and settles the run as completed.
func (l *lab) askBy(t *testing.T, run db.AgentRun, member string) {
	t.Helper()
	var from any
	if member != "" {
		from = member
	}
	d := map[string]any{"kind": "scope", "title": "Which population?", "body": "The question reads two ways.",
		"options": []any{
			map[string]any{"id": "adults", "label": "Adults", "consequence": "The study looks at adults only."},
			map[string]any{"id": "children", "label": "Children", "consequence": "The study looks at children only."},
		}, "expires_at": nil, "from_member": from}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: *run.CardID, AgentRun: run.ID, Column: run.BoardColumn, Role: run.Role, Round: run.Round},
			"hand_in", map[string]any{"agent_run_id": run.ID, "attempt": 1, "hand_in": map[string]any{"status": "ask", "answer": "I need the researcher.", "reason": nil, "decision": d}})
		return err
	})
	l.settle(t, run, "completed", "", "")
}

func (l *lab) agentDecisions(t *testing.T, card uuid.UUID) int {
	t.Helper()
	return l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'agent_decision'", card)
}

func TestLeadAskBlocksForDecision(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	member := l.man.Members[0].Name
	l.askBy(t, lead, member)
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "decision" || got.Round != c.Round || got.Redos != c.Redos {
		t.Fatalf("after the ask: %s %q round %d redos %d", got.State, got.BlockKind, got.Round, got.Redos)
	}
	var kind, raisedBy, from, dk, def, text string
	var raisedRun uuid.UUID
	var user uuid.UUID
	var days float64
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT kind, raised_by, raised_run_id, from_member, decision_kind, default_option, text, user_id,
		extract(epoch FROM expires_at - created_at) / 86400 FROM inbox_item WHERE card_id = $1 AND kind = 'agent_decision' AND status = 'open'`, c.ID).
		Scan(&kind, &raisedBy, &raisedRun, &from, &dk, &def, &text, &user, &days); err != nil {
		t.Fatal(err)
	}
	if raisedBy != "agent" || raisedRun != lead.ID || from != member || dk != "scope" || def != "" || user != l.owner ||
		!strings.HasPrefix(text, "Which population?") || days < 6.9 || days > 7.1 {
		t.Fatalf("item: %s %s %s %q %s %q %q %s %.2f", kind, raisedBy, raisedRun, from, dk, def, text, user, days)
	}
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
	if n := l.count(t, "SELECT count(*) FROM event WHERE card_id = $1 AND type = 'inbox.created'", c.ID); n != 1 {
		t.Fatalf("%d inbox.created events", n)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("the ask queued %d runs", len(q))
	}
}

// A lead that asks while one of its decisions is open fails its hand-in:
// the column is redone, and no second item is written.
func TestSecondAskWhileOpenFails(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	if _, err := l.env.Owner.Exec(context.Background(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, options, raised_by, decision_kind)
		VALUES ($1, $2, $3, 'agent_decision', 'Earlier question', '[{"id":"a","label":"A","consequence":"a"},{"id":"b","label":"B","consequence":"b"}]', 'agent', 'cost')`,
		l.ws, c.ID, l.owner); err != nil {
		t.Fatal(err)
	}
	l.askBy(t, l.latestRun(t, c.ID, "leader"), "")
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after the second ask: %s redos %d round %d", got.State, got.Redos, got.Round)
	}
	if q := l.runs(t, c.ID); len(q) != 1 || !strings.Contains(q[0].Prompt, "one decision is already open on this study") {
		t.Fatalf("the redo's run: %+v", q)
	}
}

func TestJevSaysDecideYourself(t *testing.T) {
	l := newLab(t)
	s := l.jev(t, 200, levels(nil)) // its choice is the first option by name: decide_yourself
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	asker := l.latestRun(t, c.ID, "agent")
	l.askBy(t, asker, "")
	if n := l.agentDecisions(t, c.ID); n != 0 {
		t.Fatalf("%d agent decisions", n)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.Round != c.Round || got.Redos != c.Redos {
		t.Fatalf("after decide_yourself: %s round %d redos %d", got.State, got.Round, got.Redos)
	}
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "agent" || !strings.Contains(q[0].Prompt, "Decide this yourself: Which population?") || q[0].Scope != asker.ContinuityScope {
		t.Fatalf("the lead's next run: %+v", q)
	}
	calls := s.calls()
	if len(calls) != 1 {
		t.Fatalf("%d Jev calls", len(calls))
	}
	for _, q := range calls[0].Questions {
		if q.Type != "choice" || q.Options["fits"] == "" || q.Options["decide_yourself"] == "" {
			t.Fatalf("Jev was asked %+v", q)
		}
	}
	if !strings.Contains(calls[0].State, "Which population?") || !strings.Contains(calls[0].State, "scope") {
		t.Fatalf("Jev's state: %s", calls[0].State)
	}
}

func TestNoJevSkipsFitCheck(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	l.askBy(t, l.latestRun(t, c.ID, "agent"), "")
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "decision" {
		t.Fatalf("after the ask: %s %q", got.State, got.BlockKind)
	}
}

func TestMemberCannotRaiseDirectly(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	member := l.man.Members[0].Name
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: l.ws, ID: c.ID})
		if err != nil {
			return err
		}
		a, err := teamAgent(ctx, tx, l.pack.ID, l.owner, member)
		if err != nil {
			return err
		}
		_, err = l.e.enqueue(ctx, tx, card, member, &l.man, a, "one")
		return err
	})
	before := len(l.runs(t, c.ID))
	l.askBy(t, l.latestRun(t, c.ID, member), "")
	if n := l.agentDecisions(t, c.ID); n != 0 {
		t.Fatalf("a member's ask raised %d decisions", n)
	}
	if got := l.card(t, c.ID); got.State != "working" {
		t.Fatalf("a member's ask left the study %s", got.State)
	}
	if after := len(l.runs(t, c.ID)); after != before-1 {
		t.Fatalf("a member's ask queued runs: %d before, %d after", before, after)
	}
}

// A lead raises a member's question only for a member of its column's Team;
// a study's single agent has no members to name.
func TestAskFromMemberMustBeOnTheTeam(t *testing.T) {
	ctx := context.Background()
	submit := func(l *lab, c db.Card, from string) []string {
		t.Helper()
		l.e.Kick(l.ws, c.ID)
		ref, run := l.claimOne(t, c.ID, "role")
		raw, _ := json.Marshal(map[string]any{"status": "ask", "answer": "I need the researcher.", "reason": nil, "hypotheses": []any{}, "replies": []any{},
			"decision": map[string]any{"kind": "cost", "title": "Pay for the dataset?", "body": "It costs 400 EUR.", "expires_at": nil, "from_member": from,
				"options": []any{map[string]any{"id": "pay", "label": "Pay", "consequence": "The study buys it."},
					map[string]any{"id": "skip", "label": "Skip", "consequence": "The study uses free data."}}}})
		hs := handin.HandIns{Runs: l.e.AgentRuns}
		probs, err := hs.SubmitWithMessageSeq(ctx, l.owner, ref, run.ID, raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		return probs
	}
	l := newLab(t)
	if p := submit(l, l.study(t, "hypotheses", true, nil), "nobody"); !slices.Contains(p, "decision: from_member must name one of your Team's members, or be null") {
		t.Fatalf("an unknown member: %q", p)
	}
	l = newLab(t)
	if p := submit(l, l.study(t, "hypotheses", true, nil), l.man.Members[1].Name); len(p) != 0 {
		t.Fatalf("a Team member: %q", p)
	}
	l = newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	if p := submit(l, l.study(t, "hypotheses", false, &a), "scout"); !slices.Contains(p, "decision: from_member must be null: you work this column alone") {
		t.Fatalf("a one-agent study: %q", p)
	}
}

func TestJevSaysItFits(t *testing.T) {
	l := newLab(t)
	s := l.jev(t, 200, levels(nil))
	s.choose = "fits"
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	l.askBy(t, l.latestRun(t, c.ID, "leader"), "")
	if len(s.calls()) != 1 {
		t.Fatalf("%d Jev calls", len(s.calls()))
	}
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "decision" {
		t.Fatalf("after the ask: %s %q", got.State, got.BlockKind)
	}
}

// Jev's decide_yourself sends a Team's question back to its leader once; a
// second in the same round fails the hand-in, so the two cannot loop.
func TestDecideYourselfTwiceFailsTheHandIn(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil)) // decide_yourself, the first option by name
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	first := l.latestRun(t, c.ID, "leader")
	l.askBy(t, first, "")
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "leader" || !strings.Contains(q[0].Prompt, "Decide this yourself: Which population?") || q[0].Scope != first.ContinuityScope {
		t.Fatalf("the leader's next run: %+v", q)
	}
	if got := l.card(t, c.ID); got.State != "working" || got.Round != c.Round || got.Redos != c.Redos {
		t.Fatalf("after the first decide_yourself: %s round %d redos %d", got.State, got.Round, got.Redos)
	}
	l.askBy(t, l.latestRun(t, c.ID, "leader"), "")
	if n := l.agentDecisions(t, c.ID); n != 0 {
		t.Fatalf("%d agent decisions", n)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after the second decide_yourself: %s redos %d round %d", got.State, got.Redos, got.Round)
	}
	if q := l.runs(t, c.ID); len(q) != 1 || !strings.Contains(q[0].Prompt, "Jev judged two questions in a row as the agent's own to decide") {
		t.Fatalf("the redo's run: %+v", q)
	}
}

// A Jev that fails leaves the server's checks alone, as with no Jev.
func TestJevUnavailableRaisesOnServerChecks(t *testing.T) {
	l := newLab(t)
	l.jev(t, 503, levels(nil))
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	l.askBy(t, l.latestRun(t, c.ID, "leader"), "")
	if n := l.agentDecisions(t, c.ID); n != 1 {
		t.Fatalf("%d agent decisions", n)
	}
}

func TestDecisionStateIncludesEveryQuestion(t *testing.T) {
	var d handin.DecisionIn
	if err := json.Unmarshal([]byte(`{"kind":"scope","title":"Choose scope","body":"Several questions","options":null,"question_set":{"questions":[{"id":"one","prompt":"Which population?","help_text":"Choose one","required":true,"mode":"single","options":[{"id":"a","label":"Adults","consequence":"Study adults"},{"id":"b","label":"Children","consequence":"Study children"}],"allow_other":false},{"id":"two","prompt":"Which locations?","help_text":null,"required":false,"mode":"text","options":[],"allow_other":false}]},"expires_at":null,"from_member":null}`), &d); err != nil {
		t.Fatal(err)
	}
	state := decisionState(d)
	for _, want := range []string{"Which population?", "Choose one", "Adults", "Study children", "Which locations?"} {
		if !strings.Contains(state, want) {
			t.Errorf("Jev state omitted %q: %s", want, state)
		}
	}
}

func TestJevChecksEveryQuestionInSet(t *testing.T) {
	l := newLab(t)
	s := l.jev(t, 200, levels(nil))
	s.choose = "fits"
	var d handin.DecisionIn
	if err := json.Unmarshal([]byte(`{"kind":"scope","title":"Choose scope","body":"Several questions","options":null,"question_set":{"questions":[{"id":"one","prompt":"First question?","help_text":null,"required":true,"mode":"text","options":[],"allow_other":false},{"id":"two","prompt":"Second question?","help_text":null,"required":false,"mode":"text","options":[],"allow_other":false}]},"expires_at":null,"from_member":null}`), &d); err != nil {
		t.Fatal(err)
	}
	if got := l.e.fitCheck(context.Background(), l.e.Jev, uuid.New(), "The researcher will choose the population in Decisions.", d); got != fitFits {
		t.Fatalf("fit %q", got)
	}
	calls := s.calls()
	if len(calls) != 2 {
		t.Fatalf("Jev calls: %d, want 2", len(calls))
	}
	for i, phrase := range []string{"First question?", "Second question?"} {
		if !strings.Contains(calls[i].State, phrase) || !strings.Contains(calls[i].State, "The researcher will choose the population in Decisions.") {
			t.Errorf("call %d lacks %q", i, phrase)
		}
	}
}

func TestQuestionSetPersistsAndCancels(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Scope researcher", "claude", "", "Ask the researcher.")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	run := l.latestRun(t, c.ID, "agent")
	decision := map[string]any{"kind": "scope", "title": "Choose scope", "body": "Several questions", "options": nil, "question_set": map[string]any{"questions": []any{map[string]any{"id": "details", "prompt": "Which group?", "help_text": nil, "required": true, "mode": "text", "options": []any{}, "allow_other": false}}}, "expires_at": nil, "from_member": nil}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: *run.CardID, AgentRun: run.ID, Column: run.BoardColumn, Role: run.Role, Round: run.Round}, "hand_in", map[string]any{"agent_run_id": run.ID, "attempt": 1, "hand_in": map[string]any{"status": "ask", "answer": "I need a scope.", "reason": nil, "decision": decision}})
		return err
	})
	l.settle(t, run, "completed", "", "")
	var raw []byte
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT question_set FROM inbox_item WHERE card_id=$1 AND kind='agent_decision'`, c.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Which group?") {
		t.Fatalf("question set was not saved: %s", raw)
	}
	l.in(t, func(tx *store.Tx) error {
		ids, err := tx.Q.CancelCardDecisions(context.Background(), db.CancelCardDecisionsParams{WorkspaceID: tx.WS, CardID: c.ID})
		if err == nil && len(ids) != 1 {
			t.Fatalf("cancelled %d decisions", len(ids))
		}
		return err
	})
	if got := l.count(t, `SELECT count(*) FROM inbox_item WHERE card_id=$1 AND status='cancelled'`, c.ID); got != 1 {
		t.Fatalf("cancelled decision count %d", got)
	}
}

func TestJevRejectsSetWhenAnyQuestionDoesNotFit(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jevRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		calls++
		choice := "fits"
		if strings.Contains(req.State, "Second question?") {
			choice = "decide_yourself"
		}
		answers := map[string]any{}
		for id := range req.Questions {
			answers[id] = map[string]any{"type": "choice", "choice": choice, "probabilities": map[string]float64{choice: 0.9}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": answers})
	}))
	defer srv.Close()
	e := &Engine{Jev: &jev.Client{BaseURL: srv.URL, Key: "k", Model: jev.Model}}
	var d handin.DecisionIn
	if err := json.Unmarshal([]byte(`{"kind":"scope","title":"Scope","body":"Questions","options":null,"question_set":{"questions":[{"id":"a","prompt":"First question?","help_text":null,"required":true,"mode":"text","options":[],"allow_other":false},{"id":"b","prompt":"Second question?","help_text":null,"required":false,"mode":"text","options":[],"allow_other":false}]},"expires_at":null,"from_member":null}`), &d); err != nil {
		t.Fatal(err)
	}
	if got := e.fitCheck(context.Background(), e.Jev, uuid.New(), "", d); got != fitDecide || calls != 2 {
		t.Fatalf("Jev verdict %q after %d calls", got, calls)
	}
}

// A slow Jev must share one deadline across the entire set and stop when the
// caller cancels, instead of starting another request for each question.
func TestQuestionSetFitCheckBoundsSlowJev(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 61*time.Second {
			t.Error("question set has no bounded Jev deadline")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	e := &Engine{Jev: &jev.Client{BaseURL: "http://jev.test", Key: "k", Model: jev.Model, HTTP: &http.Client{Transport: transport}}}
	d := handin.DecisionIn{QuestionSet: &handin.QuestionSetIn{Questions: make([]handin.QuestionIn, 64)}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	start := time.Now()
	if got := e.fitCheck(ctx, e.Jev, uuid.New(), "", d); got != fitUnavailable {
		t.Fatalf("verdict %q", got)
	}
	if calls != 1 {
		t.Fatalf("started %d calls after cancellation, want 1", calls)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not stop fit checks promptly")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJevRejectsLaterQuestionAfterUnavailable(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		var req jevRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		answers := map[string]any{}
		for id := range req.Questions {
			answers[id] = map[string]any{"type": "choice", "choice": "decide_yourself", "probabilities": map[string]float64{"decide_yourself": 0.9}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": answers})
	}))
	defer srv.Close()
	e := &Engine{Jev: &jev.Client{BaseURL: srv.URL, Key: "k", Model: jev.Model}}
	d := handin.DecisionIn{QuestionSet: &handin.QuestionSetIn{Questions: make([]handin.QuestionIn, 2)}}
	if got := e.fitCheck(context.Background(), e.Jev, uuid.New(), "", d); got != fitDecide || calls != 2 {
		t.Fatalf("verdict %q after %d calls", got, calls)
	}
}
