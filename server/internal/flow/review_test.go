package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
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

// groundingStudy is a study worked by one agent from Grounding to Hypotheses,
// with one Grounding criterion.
func (l *lab) groundingStudy(t *testing.T) db.Card {
	t.Helper()
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "hypotheses", a)
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE card SET grounding_criteria = '["Studies adult human cells"]' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
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
	l.item(t, c.ID, review.KindPaper, paper(paperB))
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
		if it.Label == paperB {
			t.Fatalf("the failed paper was carried: %+v", it)
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
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "low_score" || got.BoardColumn != "grounding" {
		t.Fatalf("after a second failure: %s %s %s", got.BoardColumn, got.State, got.BlockKind)
	}
	want := reviewBlockedText(c.Number, "grounding", paperB2)
	if want != "Study #"+fmt.Sprint(c.Number)+"'s Grounding column failed Jev's review twice: "+paperB2+". Comment on the study to tell the Team what to fix." {
		t.Fatalf("blocked wording: %q", want)
	}
	if items := l.inbox(t, c.ID, "blocked"); len(items) != 1 || items[0] != want {
		t.Fatalf("blocked items: %q", items)
	}
	if rs := l.reviews(t, c.ID, "grounding"); len(rs) != 2 || rs[1].Decision != review.DecideBlock {
		t.Fatalf("review records: %+v", rs)
	}
	// The owner's comment still redoes a blocked column.
	l.e.OnComment(l.ws, c.ID, l.owner, "Use a paper that exists.")
	if got := l.card(t, c.ID); got.State != "working" || got.Round != c.Round+2 {
		t.Fatalf("after the owner's comment: %s round %d", got.State, got.Round)
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
		if strings.Contains(q, "already contain the answer") {
			return 0.1
		}
		return 0.9
	})
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	note := l.item(t, c.ID, "note", map[string]any{"text": "Telomeres shorten with each division."})
	l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: "Telomere length predicts senescence onset.",
		Prediction: review.Prediction{Shape: "direction", Direction: "decrease"}, Control: review.Control{Text: "Young cells"}, Cites: []string{note}})
	l.done(t, c.ID)
	var run uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT id FROM agent_run WHERE card_id = $1 AND role = 'auditor'", c.ID).Scan(&run); err != nil {
		t.Fatalf("no auditor run: %v", err)
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: run, Column: "hypotheses", Role: "auditor", Round: c.Round},
			"audit", map[string]any{"text": "SECRET-AUDIT", "findings": []string{"SECRET-AUDIT finding"}})
		return err
	})
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
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("the reviewed column did not finish the study: %s", got.State)
	}
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
