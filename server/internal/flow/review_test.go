package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// stubSources resolves every paper and dataset except the missing ones, and
// its recall search finds nothing.
type stubSources struct{ missing map[string]bool }

func (s stubSources) Paper(_ context.Context, id string) (sources.Hit, error) {
	if s.missing[id] {
		return sources.Hit{}, sources.ErrNotFound
	}
	return sources.Hit{ID: id, Title: "Title of " + id, Abstract: "Abstract of " + id}, nil
}

func (s stubSources) Dataset(ctx context.Context, accession string) (sources.Hit, error) {
	return s.Paper(ctx, accession)
}

func (stubSources) Search(context.Context, string) ([]sources.Hit, error) { return nil, nil }

const (
	paperA  = "arxiv:2101.00001"
	paperB  = "arxiv:2101.00002"
	paperB2 = "arxiv:2101.00003"
)

// groundingStudy is a study worked by one agent from Grounding to Hypotheses.
func (l *lab) groundingStudy(t *testing.T) db.Card {
	t.Helper()
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "hypotheses", a)
	return c
}

// item writes an agent's record on the study's current column and round and
// returns its key.
func (l *lab) item(t *testing.T, id uuid.UUID, kind string, body any) string {
	t.Helper()
	c := l.card(t, id)
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	l.in(t, func(tx *store.Tx) (err error) {
		if key, err = records.NextRecordKey(context.Background(), tx, id); err != nil {
			return err
		}
		_, err = tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{WorkspaceID: l.ws, CardID: id, BoardColumn: c.BoardColumn,
			Round: c.Round, Kind: kind, Role: "agent", Key: key, Body: b})
		return err
	})
	return key
}

func paper(id string) review.PaperBody {
	return review.PaperBody{ID: id, Text: "Measures ageing in adult fibroblasts.", Cites: []string{}}
}

// reviews are the study's review records on column, oldest first.
func (l *lab) reviews(t *testing.T, id uuid.UUID, column string) []review.Result {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(), "SELECT body FROM record WHERE card_id = $1 AND kind = 'review' AND board_column = $2 ORDER BY created_at", id, column)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []review.Result
	for rows.Next() {
		var b []byte
		var r review.Result
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func nouls(calls []jevRequest) []jevRequest {
	var out []jevRequest
	for _, c := range calls {
		for _, q := range c.Questions {
			if q.Type == "noul" {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

func TestGroundingPassMovesOn(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" || got.State != "working" || got.Round != c.Round+1 || got.HandoffNote != "" {
		t.Fatalf("after a passing review: %s %s round %d note %q", got.BoardColumn, got.State, got.Round, got.HandoffNote)
	}
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 1 || rs[0].Decision != review.DecidePass || len(rs[0].Items) != 2 || rs[0].Weakest == nil {
		t.Fatalf("review records: %+v", rs)
	}
	var verdict string
	var attempt int
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT body->>'verdict', (body->>'attempt')::int FROM record WHERE card_id = $1 AND kind = 'outcome' AND board_column = 'grounding'", c.ID).
		Scan(&verdict, &attempt); err != nil || verdict != "pass" || attempt != 1 {
		t.Fatalf("outcome %q attempt %d: %v", verdict, attempt, err)
	}
	for _, call := range stub.calls() {
		for _, q := range call.Questions {
			if q.Type == "score" {
				t.Fatalf("an item-reviewed column was scored: %+v", q)
			}
		}
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'score'", c.ID); n != 0 {
		t.Fatalf("%d score records", n)
	}
}

func TestRedoKeepsPassesAndRechecksOnlyFailures(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	keyA := l.item(t, c.ID, review.KindPaper, paper(paperA))
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "grounding" || got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after a failing review: %s %s redos %d round %d", got.BoardColumn, got.State, got.Redos, got.Round)
	}
	if !strings.Contains(got.HandoffNote, paperB) || strings.Contains(got.HandoffNote, paperA) {
		t.Fatalf("the redo note: %q", got.HandoffNote)
	}
	asked := len(stub.calls())
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.withdraw(t, c.ID, keyB)
	l.done(t, c.ID)
	later := nouls(stub.calls()[asked:])
	if len(later) == 0 {
		t.Fatal("Jev was not asked about the redone paper")
	}
	for _, call := range later {
		if !strings.Contains(call.State, paperB2) || strings.Contains(call.State, paperA) {
			t.Fatalf("attempt 2 asked Jev about more than the redone paper:\n%s", call.State)
		}
	}
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[0].Decision != review.DecideRedo || rs[1].Decision != review.DecidePass || rs[1].Attempt != 2 {
		t.Fatalf("review records: %+v", rs)
	}
	var carried bool
	for _, it := range rs[1].Items {
		if it.Key == keyA {
			carried = it.Carried && it.Status == review.Pass
		}
		if it.Label == paperB && it.Status != review.Withdrawn {
			t.Fatalf("the replaced paper was not withdrawn: %+v", it)
		}
	}
	if !carried {
		t.Fatalf("paper A was not carried: %+v", rs[1].Items)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "hypotheses" {
		t.Fatalf("after the redo passed: %s", got.BoardColumn)
	}
}

func TestSecondFailureBlocks(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true, paperB2: true}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.withdraw(t, c.ID, keyB)
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "low_score" || got.BoardColumn != "grounding" {
		t.Fatalf("after a second failure: %s %s %s", got.BoardColumn, got.State, got.BlockKind)
	}
	rs := l.reviews(t, c.ID, "grounding")
	reasons := review.Reasons(rs[len(rs)-1])
	if len(reasons) == 0 || !strings.HasPrefix(reasons[0], paperB2+": ") {
		t.Fatalf("reasons: %q", reasons)
	}
	want := reviewBlockedText(c.Number, "grounding", reasons)
	if want != "Study #"+fmt.Sprint(c.Number)+"'s Grounding column failed Jev's review again after its redo. Still failing:\n- "+reasons[0]+"\nComment on the study to tell the Team what to fix." {
		t.Fatalf("blocked wording: %q", want)
	}
	if items := l.inbox(t, c.ID, "blocked"); len(items) != 1 || items[0] != want {
		t.Fatalf("blocked items: %q", items)
	}
	l.wantStopDecision(t, c.ID)
	if rs := l.reviews(t, c.ID, "grounding"); len(rs) != 2 || rs[1].Decision != review.DecideBlock {
		t.Fatalf("review records: %+v", rs)
	}
	// The owner's comment still redoes a blocked column, and the redo is
	// sent what still fails, to answer.
	l.e.OnComment(l.ws, c.ID, l.owner, "Use a paper that exists.")
	got = l.card(t, c.ID)
	if got.State != "working" || got.Round != c.Round+2 {
		t.Fatalf("after the owner's comment: %s round %d", got.State, got.Round)
	}
	if !strings.HasPrefix(got.HandoffNote, "The researcher asked for this column to be redone:\nUse a paper that exists.\n\nJev's review sent back these items:\n- "+reasons[0]) {
		t.Fatalf("the restarted round is not told what still fails:\n%s", got.HandoffNote)
	}
}

func TestUnclearCarriesForward(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	stub.setNoul(func(string) float64 { return 0.5 })
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" || got.State != "working" {
		t.Fatalf("after an unclear review: %s %s", got.BoardColumn, got.State)
	}
	if !strings.Contains(got.HandoffNote, "Unclear in Grounding") || !strings.Contains(got.HandoffNote, paperA) {
		t.Fatalf("the Hypotheses column's note: %q", got.HandoffNote)
	}
	if rs := l.reviews(t, c.ID, "grounding"); len(rs) != 1 || rs[0].Decision != review.DecideCarry {
		t.Fatalf("review records: %+v", rs)
	}
}

func TestALegacyColumnIsScoredTheOldWay(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET legacy_column = 'grounding' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("%d Jev calls", len(calls))
	}
	for _, q := range calls[0].Questions {
		if q.Type != "score" {
			t.Fatalf("a legacy column was asked a %s question", q.Type)
		}
	}
	got := l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" || got.LegacyColumn != "" {
		t.Fatalf("after the legacy column: %s, legacy %q", got.BoardColumn, got.LegacyColumn)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'review'", c.ID); n != 0 {
		t.Fatalf("%d review records on a legacy column", n)
	}
}

func TestReviewWithoutJevNeverMovesTheStudy(t *testing.T) {
	for _, tc := range []string{"503", "no key", "no sources"} {
		t.Run(tc, func(t *testing.T) {
			l := newLab(t)
			status := 503
			if tc == "no sources" {
				status = 200
			}
			l.jev(t, status, levels(nil))
			if tc != "no sources" {
				l.e.Sources = stubSources{}
			}
			if tc == "no key" {
				l.e.Jev = nil
			}
			c := l.groundingStudy(t)
			l.item(t, c.ID, review.KindPaper, paper(paperA))
			l.done(t, c.ID)
			l.e.Kick(l.ws, c.ID) // the next tick retries
			got := l.card(t, c.ID)
			if got.BoardColumn != "grounding" || got.State != "working" || got.Round != c.Round || got.Redos != 0 {
				t.Fatalf("the study moved: %s %s round %d redos %d", got.BoardColumn, got.State, got.Round, got.Redos)
			}
			if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind IN ('review', 'outcome')", c.ID); n != 0 {
				t.Fatalf("%d review or outcome records without Jev", n)
			}
			if items := l.inbox(t, c.ID, "scoring_unavailable"); len(items) != 1 || items[0] != unavailableText(c.Number, "grounding") {
				t.Fatalf("scoring_unavailable items: %q", items)
			}
		})
	}
}

func TestTheAuditorsFindingsNeverReachJev(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	// A yes to the lookup question fails a hypothesis.
	stub.setNoul(func(q string) float64 {
		if strings.Contains(q, "test of this exact prediction") {
			return 0.1
		}
		return 0.9
	})
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	note := l.groundingPaper(t, c.ID, paperA)
	l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: "Telomere length predicts senescence onset.",
		Prediction: review.Prediction{Shape: "direction", Direction: "decrease"}, Control: review.Control{Text: "Young cells"}, Cites: []string{note}})
	l.done(t, c.ID)
	var run uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT id FROM agent_run WHERE card_id = $1 AND role = 'auditor'", c.ID).Scan(&run); err != nil {
		t.Fatalf("no auditor run: %v", err)
	}
	var audit db.Record
	l.in(t, func(tx *store.Tx) (err error) {
		audit, err = records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: run, Column: "hypotheses", Role: "auditor", Round: c.Round},
			"audit", map[string]any{"text": "SECRET-AUDIT", "findings": []string{"SECRET-AUDIT finding"}})
		return err
	})
	// A hypothesis citing the audit does not bring it to Jev.
	cheat := l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: "The audit settles it.",
		Prediction: review.Prediction{Shape: "direction", Direction: "increase"}, Control: review.Control{Text: "Young cells"}, Cites: []string{audit.Key}})
	l.settleAuditor(t, c.ID)
	calls := stub.calls()
	if len(nouls(calls)) == 0 {
		t.Fatal("Jev was not asked about the hypothesis")
	}
	for _, call := range calls {
		if strings.Contains(call.State, "SECRET-AUDIT") {
			t.Fatalf("the auditor's findings reached Jev:\n%s", call.State)
		}
	}
	rs := l.reviews(t, c.ID, "hypotheses")
	if len(rs) != 1 {
		t.Fatalf("%d reviews", len(rs))
	}
	for _, it := range rs[0].Items {
		if want := review.Pass; it.Key == cheat {
			want = review.Fail
			if it.Status != want || it.Checks[0].ID != "cites_recorded" || it.Checks[0].Status != review.Fail {
				t.Fatalf("the hypothesis citing the audit: %+v", it)
			}
		} else if it.Status != want {
			t.Fatalf("hypothesis %s: %+v", it.Key, it)
		}
	}
}

func TestAHypothesisCitingItsOwnColumnFailsItsCites(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	stub.setNoul(func(q string) float64 {
		if strings.Contains(q, "test of this exact prediction") {
			return 0.1
		}
		return 0.9
	})
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	ground := l.groundingPaper(t, c.ID, paperA)
	prose := l.item(t, c.ID, "explanation", map[string]any{"text": "OWN-PROSE telomeres settle it."})
	good := l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: "Telomere length predicts senescence onset.",
		Prediction: review.Prediction{Shape: "direction", Direction: "decrease"}, Control: review.Control{Text: "Young cells"}, Cites: []string{ground}})
	bad := l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: "Our explanation settles it.",
		Prediction: review.Prediction{Shape: "direction", Direction: "increase"}, Control: review.Control{Text: "Young cells"}, Cites: []string{prose}})
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	for _, call := range stub.calls() {
		if strings.Contains(call.State, "OWN-PROSE") {
			t.Fatalf("the agents' own prose reached Jev as evidence:\n%s", call.State)
		}
	}
	rs := l.reviews(t, c.ID, "hypotheses")
	if len(rs) != 1 {
		t.Fatalf("%d reviews", len(rs))
	}
	for _, it := range rs[0].Items {
		switch it.Key {
		case good:
			if it.Status != review.Pass {
				t.Fatalf("the hypothesis citing the Grounding paper: %+v", it)
			}
		case bad:
			if it.Status != review.Fail || it.Checks[0].ID != "cites_recorded" || it.Checks[0].Status != review.Fail {
				t.Fatalf("the hypothesis citing its own column: %+v", it)
			}
		}
	}
}

// groundingPaper writes a paper as the study's Grounding column declared it
// and returns its key.
func (l *lab) groundingPaper(t *testing.T, id uuid.UUID, paperID string) string {
	t.Helper()
	b, _ := json.Marshal(paper(paperID))
	var key string
	l.in(t, func(tx *store.Tx) (err error) {
		if key, err = records.NextRecordKey(context.Background(), tx, id); err != nil {
			return err
		}
		_, err = tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{WorkspaceID: l.ws, CardID: id, BoardColumn: "grounding",
			Kind: review.KindPaper, Role: "agent", Key: key, Body: b})
		return err
	})
	return key
}

func TestExperimentationTracesValuesIntoSavedFiles(t *testing.T) {
	l := newLab(t)
	l.e.Blobs = storage.Local{Dir: t.TempDir()}
	stub := l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "experimentation", "experimentation", a)
	l.e.Kick(l.ws, c.ID)
	ctx := context.Background()
	file := "run,effect\nfit,0.123456\n"
	blob := l.ws.String() + "/outputs/fit"
	if err := l.e.Blobs.Put(ctx, blob, strings.NewReader(file), int64(len(file))); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "experimentation", Round: c.Round})
		if err != nil || len(runs) == 0 {
			return fmt.Errorf("no agent run: %v", err)
		}
		run := runs[0].ID
		for _, r := range []db.InsertRecordParams{
			{Kind: "run", Role: "server", Key: "x1", Body: []byte(`{"tool":"run_script","output":"saved results/fit.csv\n","exit_status":0}`), AgentRunID: &run},
			{Kind: "output", Role: "agent", Key: "o1", BlobKey: blob, AgentRunID: &run,
				Body: []byte(fmt.Sprintf(`{"name":"results/fit.csv","size_bytes":%d,"content_type":"text/csv"}`, len(file)))},
			{Kind: "result", Role: "agent", Key: "r1", Body: []byte(`{"text":"The effect is 0.12.","cites":["run:x1"],"value":0.12}`)},
			{Kind: "result", Role: "agent", Key: "r2", Body: []byte(`{"text":"The control is 7.","cites":["run:x1"],"value":7}`)},
			{Kind: review.KindOutputs, Role: "agent", Key: "r3", Body: []byte(`{"files":["results/fit.csv"]}`)},
		} {
			r.WorkspaceID, r.CardID, r.BoardColumn, r.Round = l.ws, c.ID, "experimentation", c.Round
			if _, err := tx.Q.InsertRecord(ctx, r); err != nil {
				return err
			}
		}
		return nil
	})
	l.done(t, c.ID)
	rs := l.reviews(t, c.ID, "experimentation")
	if len(rs) != 1 || rs[0].Decision != review.DecideRedo {
		t.Fatalf("review records: %+v", rs)
	}
	status := map[string]string{}
	for _, it := range rs[0].Items {
		status[it.Key] = it.Status + " " + it.Checks[0].Detail
	}
	if status["r3"] != "pass " || status["r1"] != "pass output:o1" || !strings.HasPrefix(status["r2"], "fail ") {
		t.Fatalf("items: %v", status)
	}
	described := nouls(stub.calls())
	if len(described) != 1 || !strings.Contains(described[0].State, "fit,0.123456") || !strings.Contains(described[0].State, "The effect is 0.12.") {
		t.Fatalf("Jev was not shown the value in its file: %d calls", len(described))
	}
}

func TestAroundCutsOnCharacterBoundaries(t *testing.T) {
	out := strings.Repeat("é", 200) + " effect: 0.123456 " + strings.Repeat("ü", 200)
	s := around(out, "0.12")
	if !utf8.ValidString(s) || len(s) > 300 || !strings.Contains(s, "0.123456") {
		t.Fatalf("around = %q (%d bytes)", s, len(s))
	}
	if around(out, "0.99") != "" {
		t.Fatal("a value the text does not hold was found")
	}
}

// withdraw writes the agent's withdrawal of the item record key.
func (l *lab) withdraw(t *testing.T, id uuid.UUID, key string) string {
	t.Helper()
	return l.item(t, id, review.KindWithdrawn, map[string]any{"item": key, "reason": "It does not resolve."})
}

func TestAnUnansweredFailureIsCarriedAsFailed(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.done(t, c.ID) // the redo answers nothing
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[1].Decision != review.DecideBlock || len(rs[1].Items) != 2 {
		t.Fatalf("review records: %+v", rs)
	}
	for _, it := range rs[1].Items {
		if it.Key == keyB && (it.Status != review.Fail || !it.Carried || it.Checks[0].ID != "exists") {
			t.Fatalf("the unanswered failure: %+v", it)
		}
	}
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "low_score" {
		t.Fatalf("after an unanswered failure: %s %s", got.State, got.BlockKind)
	}
}

func TestCorrectedValueNeedsTheLeadsExplicitReply(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Codex tester", "codex", "", "")
	c := l.scoped(t, "experimentation", "experimentation", a)
	old := l.put(t, c.ID, "experimentation", 1, "value", "agent", map[string]any{"name": "slope", "value": "0.42", "file": "fit.py"})
	l.put(t, c.ID, "experimentation", 1, "review", "server", review.Result{Column: "experimentation", Items: []review.Item{{Key: old, Kind: "value", Label: "0.42: slope", Identity: old + " 0.42", Status: review.Fail}}})
	c = l.move(t, c.ID, "experimentation", 2)
	l.e.Kick(l.ws, c.ID)
	ctx := context.Background()
	runtimes, err := l.e.AgentRuns.Register(ctx, l.owner, l.ws, "codex-test", "test", []string{"codex"}, nil, agent.Platform{})
	if err != nil || len(runtimes) != 1 {
		t.Fatalf("register: %v", err)
	}
	ref := service.RuntimeRef{WorkspaceID: l.ws, RuntimeID: runtimes[0].ID}
	claims, err := l.e.AgentRuns.Claim(ctx, l.owner, []service.RuntimeRef{ref}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %v", err)
	}
	run := claims[0].AgentRun
	ref.Attempt = run.Attempt
	if err := l.e.AgentRuns.Start(ctx, l.owner, ref, run.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "experimentation", Round: 2, Kind: "run", Role: "server", Key: "x1", AgentRunID: &run.ID, Body: []byte(`{"tool":"run_script","path":"fit.py","output":"0.43","exit_status":0}`)})
		return err
	})
	unanswered := l.putBy(t, run, "value", map[string]any{"name": "slope", "value": "0.43", "file": "fit.py"})
	rv, err := l.e.readReview(context.Background(), l.ws, c)
	if err != nil || len(rv.carried) != 1 || rv.carried[0].Status != review.Fail {
		t.Fatalf("unanswered: %+v, %v", rv.carried, err)
	}
	if _, err := l.env.Owner.Exec(ctx, `UPDATE record SET superseded_at = now() WHERE card_id = $1 AND key = $2`, c.ID, unanswered); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"status":"done","answer":"Corrected the slope.","values":[{"name":"slope","value":"0.43","file":"fit.py"}],"outputs":[],"replies":[{"item":"0.42: slope","action":"corrected","reason":"Recalculated."}]}`)
	hs := handin.HandIns{Runs: l.e.AgentRuns, SentBack: l.e.SentBack}
	if problems, err := hs.Submit(ctx, l.owner, ref, run.ID, raw); err != nil || len(problems) != 0 {
		t.Fatalf("submit: %v %v", problems, err)
	}
	rv, err = l.e.readReview(context.Background(), l.ws, c)
	if err != nil || len(rv.carried) != 0 {
		t.Fatalf("explicit correction: %+v, %v", rv.carried, err)
	}
	result, err := l.e.runReview(ctx, l.ws, scoring{card: c, attempt: 2}, rv)
	if err != nil || len(result.Items) != 1 || result.Items[0].Status != review.Pass || result.Items[0].Checks[0].Detail == "" {
		t.Fatalf("corrected value review: %+v, %v", result.Items, err)
	}
}

func TestCorrectedValueRequiresAnUnambiguousLeadHandIn(t *testing.T) {
	run, other := uuid.New(), uuid.New()
	old := valueRec("r1", "value", map[string]any{"name": "slope", "value": "0.42"})
	item := review.Item{Key: "r1", Kind: "value", Label: "0.42: slope", Identity: "r1 0.42", Status: review.Fail}
	prev := review.Result{Items: []review.Item{item}}
	value := valueRec("r2", "value", map[string]any{"name": "slope", "value": "0.43"})
	value.ID, value.Round, value.AgentRunID = uuid.New(), 2, &run
	body, _ := json.Marshal(map[string]any{"attempt": 1, "converted_records": []uuid.UUID{value.ID}, "hand_in": map[string]any{"status": "done", "values": []any{map[string]any{"name": "slope", "value": "0.43"}}, "replies": []any{map[string]any{"item": "slope", "action": "corrected"}}}})
	hand := db.Record{Kind: "hand_in", Round: 2, Role: "agent", AgentRunID: &run, Body: body}
	attempts := map[uuid.UUID]int32{run: 1}
	if !correctedValue(item, prev, []db.Record{old, hand}, []db.Record{value}, db.Card{Round: 2}, attempts) {
		t.Fatal("valid explicit correction was not recognized")
	}
	for _, tc := range []struct {
		name  string
		hand  db.Record
		value db.Record
	}{
		{"member", func() db.Record { h := hand; h.Role = "member"; return h }(), value},
		{"other run", hand, func() db.Record { v := value; v.AgentRunID = &other; return v }()},
		{"wrong round", func() db.Record { h := hand; h.Round = 1; return h }(), value},
		{"different value", hand, func() db.Record { v := value; v.Body = []byte(`{"name":"slope","value":"999"}`); return v }()},
		{"no reply", func() db.Record {
			h := hand
			h.Body = []byte(`{"hand_in":{"status":"done","values":[{"name":"slope","value":"0.43"}],"replies":[]}}`)
			return h
		}(), value},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if correctedValue(item, prev, []db.Record{old, tc.hand}, []db.Record{tc.value}, db.Card{Round: 2}, attempts) {
				t.Fatal("untrusted or unanswered correction accepted")
			}
		})
	}
	second := valueRec("r3", "value", map[string]any{"name": "slope", "value": "0.5"})
	prev.Items = append(prev.Items, review.Item{Key: "r3", Kind: "value", Label: "0.5: slope", Identity: "r3 0.5", Status: review.Fail})
	if correctedValue(item, prev, []db.Record{old, second, hand}, []db.Record{value}, db.Card{Round: 2}, attempts) {
		t.Fatal("ambiguous value name answered multiple failed items")
	}
	prev.Items = prev.Items[:1]
	newHand := hand
	newHand.Body = []byte(`{"attempt":2,"hand_in":{"status":"done","values":[{"name":"slope","value":"0.43"}],"replies":[]}}`)
	if correctedValue(item, prev, []db.Record{old, hand, newHand}, []db.Record{value}, db.Card{Round: 2}, map[uuid.UUID]int32{run: 2}) {
		t.Fatal("an earlier hand-in answered a later attempt")
	}
	newBody, _ := json.Marshal(map[string]any{"attempt": 2, "converted_records": []uuid.UUID{uuid.New()}, "hand_in": map[string]any{"status": "done", "values": []any{map[string]any{"name": "slope", "value": "0.43"}}, "replies": []any{map[string]any{"item": "slope", "action": "corrected"}}}})
	newHand.Body = newBody
	if correctedValue(item, prev, []db.Record{old, hand, newHand}, []db.Record{value}, db.Card{Round: 2}, map[uuid.UUID]int32{run: 2}) {
		t.Fatal("a previous attempt's value was paired with the current hand-in")
	}
}

func TestAWithdrawnFailureLeavesTheRest(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	keyA := l.item(t, c.ID, review.KindPaper, paper(paperA))
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.withdraw(t, c.ID, keyB)
	l.withdraw(t, c.ID, keyA) // A passed: withdrawing it is ignored
	l.done(t, c.ID)
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[1].Decision != review.DecidePass || len(rs[1].Items) != 2 {
		t.Fatalf("review records: %+v", rs)
	}
	for _, it := range rs[1].Items {
		switch it.Key {
		case keyA:
			if it.Status != review.Pass || !it.Carried {
				t.Fatalf("paper A: %+v", it)
			}
		case keyB:
			want := review.Check{ID: "withdrawn", By: "agent", Detail: "It does not resolve.", Status: review.Withdrawn}
			if it.Status != review.Withdrawn || len(it.Checks) != 1 || it.Checks[0] != want {
				t.Fatalf("the withdrawn paper: %+v", it)
			}
		default:
			t.Fatalf("item %+v", it)
		}
	}
	if got := l.card(t, c.ID); got.BoardColumn != "hypotheses" || got.State != "working" {
		t.Fatalf("after the withdrawn failure: %s %s", got.BoardColumn, got.State)
	}
}

func TestARedeclaredFailureIsReviewedAgain(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	// B resolves now; the redo declares it again, in capitals.
	l.e.Sources = stubSources{}
	asked := len(stub.calls())
	again := l.item(t, c.ID, review.KindPaper, paper(strings.ToUpper(paperB)))
	l.done(t, c.ID)
	later := nouls(stub.calls()[asked:])
	if len(later) != 1 || !strings.Contains(later[0].State, strings.ToUpper(paperB)) {
		t.Fatalf("the redeclared paper was not asked again: %d calls", len(later))
	}
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[1].Decision != review.DecidePass || len(rs[1].Items) != 2 {
		t.Fatalf("review records: %+v", rs)
	}
	for _, it := range rs[1].Items {
		if it.Key == again && (it.Carried || it.Status != review.Pass) {
			t.Fatalf("the redeclared paper: %+v", it)
		}
	}
}

func TestNothingLeftIsNeverAPass(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.withdraw(t, c.ID, keyB) // the redo withdraws its only item
	l.done(t, c.ID)
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[0].Decision != review.DecideRedo || rs[1].Decision != review.DecideAsk || len(rs[1].Items) != 1 || rs[1].Items[0].Status != review.Withdrawn {
		t.Fatalf("review records: %+v", rs)
	}
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "" || got.BoardColumn != "grounding" || got.Redos != 1 {
		t.Fatalf("after an empty redo: %s %q %s redos %d", got.State, got.BlockKind, got.BoardColumn, got.Redos)
	}
	if items := l.inbox(t, c.ID, "needs_decision"); len(items) != 1 || !strings.HasPrefix(items[0], "The Team withdrew all 1 item: It does not resolve. Nothing is left to review.") {
		t.Fatalf("needs_decision items: %q", items)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND cause = 'all_withdrawn' AND resolved_at IS NULL", c.ID); n != 1 {
		t.Fatalf("%d open all_withdrawn items", n)
	}
	l.wantStopDecision(t, c.ID)
}

func TestARedeclaredPassStands(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	// Asked again, A would now fail: it no longer resolves.
	l.e.Sources = stubSources{missing: map[string]bool{paperA: true, paperB: true}}
	asked := len(stub.calls())
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.withdraw(t, c.ID, keyB)
	l.done(t, c.ID)
	for _, call := range nouls(stub.calls()[asked:]) {
		if strings.Contains(call.State, paperA) {
			t.Fatalf("the carried paper was asked again:\n%s", call.State)
		}
	}
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[1].Decision != review.DecidePass {
		t.Fatalf("review records: %+v", rs)
	}
	n := 0
	for _, it := range rs[1].Items {
		if it.Label == paperA {
			n++
			if !it.Carried || it.Status != review.Pass {
				t.Fatalf("paper A: %+v", it)
			}
		}
	}
	if n != 1 || len(rs[1].Items) != 3 {
		t.Fatalf("paper A counted %d times among %+v", n, rs[1].Items)
	}
}

func TestARoundsItemsCountOnceWhateverTheirCase(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	asked := len(stub.calls())
	l.withdraw(t, c.ID, keyB)
	// The redo names the passed paper again in capitals, and declares its
	// new paper twice.
	l.item(t, c.ID, review.KindPaper, paper(strings.ToUpper(paperA)))
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.done(t, c.ID)
	later := nouls(stub.calls()[asked:])
	for _, call := range later {
		if strings.Contains(call.State, strings.ToUpper(paperA)) {
			t.Fatalf("the carried paper was asked again:\n%s", call.State)
		}
	}
	if len(later) != 1 {
		t.Fatalf("Jev was asked %d times about the redo, want once", len(later))
	}
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || len(rs[1].Items) != 3 {
		t.Fatalf("review records: %+v", rs)
	}
}

func TestExperimentationRedoCarriesPassedValues(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "experimentation", "experimentation", a)
	ctx := context.Background()
	insert := func(recs ...db.InsertRecordParams) {
		t.Helper()
		l.e.Kick(l.ws, c.ID)
		cur := l.card(t, c.ID)
		l.in(t, func(tx *store.Tx) error {
			for _, r := range recs {
				r.WorkspaceID, r.CardID, r.BoardColumn, r.Round = l.ws, c.ID, "experimentation", cur.Round
				if _, err := tx.Q.InsertRecord(ctx, r); err != nil {
					return err
				}
			}
			return nil
		})
	}
	insert(
		db.InsertRecordParams{Kind: "output", Role: "agent", Key: "o1", Body: []byte(`{"name":"fit.csv","size_bytes":3,"content_type":"text/csv"}`)},
		db.InsertRecordParams{Kind: "run", Role: "server", Key: "x1", Body: []byte(`{"tool":"run_script","output":"effect 0.123456\n","exit_status":0}`)},
		db.InsertRecordParams{Kind: "run", Role: "server", Key: "x2", Body: []byte(`{"tool":"run_script","output":"control 7\n","exit_status":1}`)},
		db.InsertRecordParams{Kind: "result", Role: "agent", Key: "r1", Body: []byte(`{"text":"The effect is 0.12.","cites":["run:x1"],"value":0.12}`)},
		db.InsertRecordParams{Kind: "result", Role: "agent", Key: "r2", Body: []byte(`{"text":"The control is 7.","cites":["run:x2"],"value":7}`)},
		db.InsertRecordParams{Kind: review.KindOutputs, Role: "agent", Key: "r3", Body: []byte(`{"files":["fit.csv"]}`)},
	)
	l.done(t, c.ID)
	insert(
		db.InsertRecordParams{Kind: "run", Role: "server", Key: "x3", Body: []byte(`{"tool":"run_script","output":"control 7\n","exit_status":0}`)},
		db.InsertRecordParams{Kind: "result", Role: "agent", Key: "r4", Body: []byte(`{"text":"The control is 7.","cites":["run:x3"],"value":7}`)},
		db.InsertRecordParams{Kind: review.KindWithdrawn, Role: "agent", Key: "r5", Body: []byte(`{"item":"r2","reason":"Replaced by r4."}`)},
	)
	l.done(t, c.ID)
	rs := l.reviews(t, c.ID, "experimentation")
	if len(rs) != 2 || rs[0].Decision != review.DecideRedo || rs[1].Decision != review.DecidePass {
		t.Fatalf("review records: %+v", rs)
	}
	got := map[string]string{}
	for _, it := range rs[1].Items {
		got[it.Key] = fmt.Sprintf("%s carried=%v", it.Status, it.Carried)
	}
	if got["r1"] != "pass carried=true" || got["r4"] != "pass carried=false" || got["r3"] != "pass carried=false" ||
		got["r2"] != "withdrawn carried=true" || len(got) != 4 {
		t.Fatalf("attempt 2's items: %v", got)
	}
	if card := l.card(t, c.ID); card.State != "done" {
		t.Fatalf("the redone column did not finish: %s", card.State)
	}
}

func TestASecondScoreOfAReviewedRoundIsANoOp(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	// Put the study back where it was reviewed, as a pass that read it
	// before the review was written would see it.
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET board_column = 'grounding', state = 'working', round = $2, redos = 0 WHERE id = $1", c.ID, c.Round); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Score(context.Background(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.e.applyReview(context.Background(), l.ws, scoring{card: l.card(t, c.ID), attempt: 1},
		review.Result{Column: "grounding", Items: []review.Item{{Key: "r9", Status: review.Pass}}}, nil); err != nil {
		t.Fatal(err)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind IN ('review', 'outcome')", c.ID); n != 2 {
		t.Fatalf("%d review and outcome records, want one of each", n)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "grounding" || got.Round != c.Round {
		t.Fatalf("a second pass moved the study: %s round %d", got.BoardColumn, got.Round)
	}
}

func TestALongReviewKeepsItsLease(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	makeRetry, retries := l.e.Retry, 0
	l.e.Retry = func() *jev.Retry { retries++; return makeRetry() }
	l.e.Sources = stubSources{}
	start := time.Now().Truncate(time.Microsecond)
	var clock atomic.Int64
	l.e.Now = func() time.Time { return start.Add(time.Duration(clock.Load())) }
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	// Each of Jev's answers takes four minutes; the review outlives the
	// lease it was read under unless it extends it.
	var leases []time.Time
	stub.setNoul(func(string) float64 {
		clock.Add(int64(4 * time.Minute))
		var until *time.Time
		if err := l.env.Owner.QueryRow(context.Background(), "SELECT gating_until FROM card WHERE id = $1", c.ID).Scan(&until); err != nil || until == nil {
			t.Errorf("no lease mid-review: %v", err)
		} else {
			leases = append(leases, *until)
		}
		return 0.9
	})
	l.done(t, c.ID)
	if got := l.card(t, c.ID); got.BoardColumn != "hypotheses" || got.GatingUntil != nil {
		t.Fatalf("after the review: %s, lease %v", got.BoardColumn, got.GatingUntil)
	}
	if len(leases) != 3 {
		t.Fatalf("%d leases seen", len(leases))
	}
	// The third paper was looked up at minute 8.
	if now := l.e.now(); leases[2].Before(start.Add(8*time.Minute + scoringLease)) {
		t.Fatalf("the lease was not extended: it runs to %v at the third answer (%v)", leases[2], now)
	}
	if retries != 1 {
		t.Fatalf("one review made %d retry budgets", retries)
	}
}

func TestHypothesesSharingTheirOpeningAreEachReviewed(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	ground := l.groundingPaper(t, c.ID, paperA)
	opening := "Telomere length in adult human fibroblasts measured across many passages under standard culture predicts "
	keys := map[string]bool{}
	for _, end := range []string{"senescence onset.", "apoptosis rate."} {
		keys[l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: opening + end,
			Prediction: review.Prediction{Shape: "direction", Direction: "decrease"}, Control: review.Control{Text: "Young cells"}, Cites: []string{ground}})] = true
	}
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	if n := len(nouls(stub.calls())); n != 2 {
		t.Fatalf("Jev was asked about %d hypotheses, want 2", n)
	}
	rs := l.reviews(t, c.ID, "hypotheses")
	if len(rs) != 1 || len(rs[0].Items) != 2 {
		t.Fatalf("review records: %+v", rs)
	}
	for _, it := range rs[0].Items {
		if !keys[it.Key] || it.Identity == "" {
			t.Fatalf("item %+v", it)
		}
	}
}

func TestAReviewWithoutIdentitiesMatchesByLabel(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	keyA := l.item(t, c.ID, review.KindPaper, paper(paperA))
	keyB := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	// The review as it was stored before items carried their identity.
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE record SET body = jsonb_set(body, '{items}',
		(SELECT jsonb_agg(i - 'identity') FROM jsonb_array_elements(body->'items') i)) WHERE card_id = $1 AND kind = 'review'`, c.ID); err != nil {
		t.Fatal(err)
	}
	if rs := l.reviews(t, c.ID, "grounding"); len(rs) != 1 || rs[0].Items[0].Identity != "" {
		t.Fatalf("the stored review still has identities: %+v", rs)
	}
	asked := len(stub.calls())
	l.item(t, c.ID, review.KindPaper, paper(strings.ToUpper(paperA)))
	l.done(t, c.ID)
	for _, call := range nouls(stub.calls()[asked:]) {
		if strings.Contains(call.State, strings.ToUpper(paperA)) {
			t.Fatalf("the passed paper was asked again:\n%s", call.State)
		}
	}
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[1].Decision != review.DecideBlock || len(rs[1].Items) != 2 {
		t.Fatalf("review records: %+v", rs)
	}
	for _, it := range rs[1].Items {
		switch it.Key {
		case keyA:
			if it.Status != review.Pass || !it.Carried {
				t.Fatalf("paper A: %+v", it)
			}
		case keyB:
			if it.Status != review.Fail || !it.Carried {
				t.Fatalf("paper B: %+v", it)
			}
		default:
			t.Fatalf("item %+v", it)
		}
	}
}

func TestAllWithdrawnAsksTheOwner(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	stub.setNoul(func(q string) float64 {
		if strings.Contains(q, "test of this exact prediction") {
			return 0.1
		}
		return 0.9
	})
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	l.groundingPaper(t, c.ID, paperA)
	prose := l.item(t, c.ID, "explanation", map[string]any{"text": "Our own prose."})
	var keys []string
	for _, text := range []string{"First idea.", "Second idea.", "Third idea."} {
		keys = append(keys, l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: text,
			Prediction: review.Prediction{Shape: "direction", Direction: "increase"}, Control: review.Control{Text: "Young cells"}, Cites: []string{prose}}))
	}
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	if got := l.card(t, c.ID); got.Round != c.Round+1 || got.Redos != 1 || got.State != "working" {
		t.Fatalf("after the first failure: %s round %d redos %d", got.State, got.Round, got.Redos)
	}
	for i, k := range keys {
		l.item(t, c.ID, review.KindWithdrawn, map[string]any{"item": k, "reason": fmt.Sprintf("reason %d", i+1)})
	}
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	if rs := l.reviews(t, c.ID, "hypotheses"); len(rs) != 2 || rs[1].Decision != review.DecideAsk {
		t.Fatalf("review records: %+v", rs)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'outcome' AND body->>'verdict' = 'ask'", c.ID); n != 1 {
		t.Fatalf("%d ask outcomes", n)
	}
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "" || got.Redos != 1 || got.BoardColumn != "hypotheses" {
		t.Fatalf("held study: %s %q redos %d column %s", got.State, got.BlockKind, got.Redos, got.BoardColumn)
	}
	items := l.inbox(t, c.ID, "needs_decision")
	want := "The Team withdrew all 3 hypotheses: reason 1; reason 2; reason 3. Nothing is left to test. Comment with guidance to redo the column, or close the study as a negative result."
	if len(items) != 1 || items[0] != want {
		t.Fatalf("needs_decision items: %q", items)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND cause = 'all_withdrawn' AND resolved_at IS NULL", c.ID); n != 1 {
		t.Fatalf("%d open all_withdrawn items", n)
	}
	l.e.OnComment(l.ws, c.ID, l.owner, "Try a different angle.")
	got = l.card(t, c.ID)
	if got.State != "working" || got.Round != c.Round+2 || got.Redos != 1 {
		t.Fatalf("after the owner's comment: %s round %d redos %d", got.State, got.Round, got.Redos)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND cause = 'all_withdrawn' AND resolved_at IS NULL", c.ID); n != 0 {
		t.Fatalf("the comment left %d all_withdrawn items open", n)
	}
}

func TestPassedWithOpenDoubts(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	stub.setNoul(func(string) float64 { return 0.52 })
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" || !strings.Contains(got.HandoffNote, "Unclear in Grounding") {
		t.Fatalf("after an unclear review: %s %q", got.BoardColumn, got.HandoffNote)
	}
	if items := l.inbox(t, c.ID, "open_doubts"); len(items) != 0 {
		t.Fatalf("open_doubts items: %q", items)
	}
}

// searchSources finds hits on every recall search.
type searchSources struct {
	stubSources
	hits []sources.Hit
}

func (s searchSources) Search(context.Context, string) ([]sources.Hit, error) { return s.hits, nil }

func TestDeclinedMissedPaperReachesTheReview(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.e.Sources = searchSources{hits: []sources.Hit{{ID: "openalex:W9", DOI: "10.1000/abc", Title: "Declined candidate", Abstract: "An abstract."}}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	// Written as the hand-in writes it: the id in the form the agent gave.
	l.item(t, c.ID, "declined", map[string]string{"item": handin.NormID("https://doi.org/10.1000/ABC"), "reason": "It studies yeast."})
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.CreateComment(context.Background(), db.CreateCommentParams{WorkspaceID: l.ws, CardID: c.ID, AuthorID: l.owner, Body: "Mammalian cells only."})
		return err
	})
	l.done(t, c.ID)
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 1 || len(rs[0].Missed) != 0 {
		t.Fatalf("the declined paper stayed missed: %+v", rs)
	}
	var state string
	for _, call := range nouls(stub.calls()) {
		for _, q := range call.Questions {
			if strings.Contains(q.Instructions, "outside the study's scope") {
				state = call.State
			}
		}
	}
	if state == "" || !strings.Contains(state, "Mammalian cells only.") || !strings.Contains(state, "It studies yeast.") {
		t.Fatalf("the out_of_scope state lacks the comment or reason:\n%s", state)
	}
}
