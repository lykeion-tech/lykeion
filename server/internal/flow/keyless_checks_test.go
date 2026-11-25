package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/review"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type tracedReviewSources struct {
	stubSources
	retracted   map[string]bool
	paperErrors map[string]error
	searchErr   error
	recall      []sources.Hit
	calls       []string
}

func (s *tracedReviewSources) Paper(ctx context.Context, id string) (sources.Hit, error) {
	s.calls = append(s.calls, "paper:"+id)
	if err := s.paperErrors[id]; err != nil {
		return sources.Hit{}, err
	}
	hit, err := s.stubSources.Paper(ctx, id)
	hit.Retracted = s.retracted[id]
	return hit, err
}

func (s *tracedReviewSources) Search(_ context.Context, query string) ([]sources.Hit, error) {
	s.calls = append(s.calls, "search:"+query)
	for _, hit := range s.recall {
		s.calls = append(s.calls, "candidate:"+hit.ID)
	}
	return s.recall, s.searchErr
}

type checkInbox struct {
	ID      uuid.UUID
	Status  string
	Options []struct{ ID string }
}

func captureChecks(t *testing.T, l *lab, id uuid.UUID, phase string, sources *tracedReviewSources) []checkInbox {
	t.Helper()
	card := l.card(t, id)
	var recordsJSON, inboxJSON []byte
	ctx := context.Background()
	if err := l.env.Owner.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'key', key, 'column', board_column, 'round', round, 'kind', kind, 'role', role,
		'superseded', superseded_at IS NOT NULL, 'body', body) ORDER BY created_at, key), '[]')
		FROM record WHERE card_id = $1 AND kind IN ('review', 'outcome', 'score', 'validation_status', 'objection', 'route_back')`, id).Scan(&recordsJSON); err != nil {
		t.Fatal(err)
	}
	if err := l.env.Owner.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'id', id, 'kind', kind, 'cause', cause, 'text', text, 'options', options, 'status', status,
		'chosen', chosen_option, 'resolved', resolved_at IS NOT NULL) ORDER BY created_at, id), '[]')
		FROM inbox_item WHERE card_id = $1`, id).Scan(&inboxJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: column=%s state=%s round=%d redos=%d block=%q handoff=%q", phase,
		card.BoardColumn, card.State, card.Round, card.Redos, card.BlockKind, card.HandoffNote)
	t.Logf("%s: records=%s", phase, recordsJSON)
	t.Logf("%s: inbox=%s", phase, inboxJSON)
	var inbox []checkInbox
	if err := json.Unmarshal(inboxJSON, &inbox); err != nil {
		t.Fatal(err)
	}
	if sources != nil {
		t.Logf("%s: sources=%q", phase, sources.calls)
	}
	return inbox
}

func tryCheckApproval(t *testing.T, l *lab, id uuid.UUID, inbox []checkInbox, sources *tracedReviewSources) (bool, error) {
	t.Helper()
	for _, i := range inbox {
		if i.Status != "open" || !slices.ContainsFunc(i.Options, func(o struct{ ID string }) bool { return o.ID == "approve" }) {
			continue
		}
		err := l.e.Decide(context.Background(), l.ws, i.ID, l.owner, "approve", "")
		t.Logf("Decide(approve, empty guidance): %v", err)
		captureChecks(t, l, id, "after approval", sources)
		return true, err
	}
	t.Log("approval was not offered")
	return false, nil
}

func assertServerFailure(t *testing.T, l *lab, id uuid.UUID, column string, round int32, checks ...string) {
	t.Helper()
	rs := l.reviews(t, id, column)
	for _, want := range checks {
		found := false
		for _, r := range rs {
			for _, item := range r.Items {
				found = found || slices.ContainsFunc(item.Checks, func(c review.Check) bool {
					return c.ID == want && c.By == "server" && c.Status == review.Fail
				})
			}
		}
		if !found {
			t.Errorf("expected recorded failed server check %q; review records=%d", want, len(rs))
		}
	}
	got := l.card(t, id)
	if got.BoardColumn != column || (got.State != "blocked" && !(got.State == "working" && got.Round == round+1 && got.Redos == 1)) {
		t.Errorf("failed server check should stop or redo %s; got %s/%s round=%d redos=%d",
			column, got.BoardColumn, got.State, got.Round, got.Redos)
	}
	for _, kind := range []string{"needs_decision", "blocked", "scoring_unavailable", "done"} {
		for _, message := range l.inbox(t, id, kind) {
			if strings.Contains(message, "passed the server's checks") {
				t.Errorf("misleading inbox text: %q", message)
			}
		}
	}
}

func TestServerChecksWithOwnerJevAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		order  []string
		bad    string
		recall bool
		keyed  bool
	}{
		{name: "good_only", order: []string{paperA}},
		{name: "good_then_retracted", order: []string{paperA, paperB}, bad: "not_retracted"},
		{name: "retracted_then_good", order: []string{paperB, paperA}, bad: "not_retracted"},
		{name: "good_then_missing", order: []string{paperA, paperB}, bad: "exists"},
		{name: "missing_then_good", order: []string{paperB, paperA}, bad: "exists"},
		{name: "only_bad_empty_recall", order: []string{paperB, paperB2}, bad: "not_retracted"},
		{name: "only_bad_with_recall", order: []string{paperB, paperB2}, bad: "not_retracted", recall: true},
		{name: "jev_good_then_retracted", order: []string{paperA, paperB}, bad: "not_retracted", keyed: true},
		{name: "jev_retracted_then_good", order: []string{paperB, paperA}, bad: "not_retracted", keyed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			l.e.Jev = nil
			var callCount func() int
			if tc.keyed {
				stub := l.jev(t, 200, levels(nil))
				stub.setNoul(func(string) float64 { return 1 })
				callCount = func() int { return len(stub.calls()) }
			}
			src := &tracedReviewSources{stubSources: stubSources{missing: map[string]bool{paperB: tc.bad == "exists", paperB2: true}},
				retracted: map[string]bool{paperB: tc.bad == "not_retracted"}}
			if tc.recall {
				src.recall = []sources.Hit{{ID: "arxiv:2101.00004", Title: "Recall candidate"}, {ID: "arxiv:2101.00005", Title: "Second recall candidate"}}
			}
			l.e.Sources = src
			c := l.groundingStudy(t)
			for _, id := range tc.order {
				l.item(t, c.ID, review.KindPaper, paper(id))
			}
			l.done(t, c.ID)
			l.e.Kick(l.ws, c.ID)
			if callCount != nil {
				t.Logf("fake Jev calls: %d", callCount())
				if callCount() != 1 {
					t.Errorf("expected one call for the good paper; got %d", callCount())
				}
			}
			inbox := captureChecks(t, l, c.ID, "before approval", src)
			var lookedUp []string
			for _, call := range src.calls {
				if id, ok := strings.CutPrefix(call, "paper:"); ok {
					lookedUp = append(lookedUp, id)
				}
			}
			if !slices.Equal(lookedUp, tc.order) {
				t.Errorf("expected all submitted lookups in order %v; got %v", tc.order, lookedUp)
			}
			if tc.bad == "" {
				got := l.card(t, c.ID)
				if got.State != "blocked" || got.Round != c.Round || got.Redos != 0 {
					t.Errorf("expected owner gate in original round; got %+v", got)
				}
				if messages := l.inbox(t, c.ID, "needs_decision"); len(messages) != 1 ||
					!strings.Contains(messages[0], "Server checks ran at ") ||
					!strings.Contains(messages[0], "Recall search ran. 0 candidate(s) were not screened.") {
					t.Errorf("expected the ordinary missing-key decision; got %q", messages)
				}
				offered, err := tryCheckApproval(t, l, c.ID, inbox, src)
				got = l.card(t, c.ID)
				if !offered || err != nil || got.BoardColumn != "hypotheses" || got.Round != c.Round+1 {
					t.Errorf("clean control should advance on approval; offered=%t err=%v column=%s round=%d", offered, err, got.BoardColumn, got.Round)
				}
				return
			}

			checks := []string{tc.bad}
			if slices.Contains(tc.order, paperB2) {
				checks = append(checks, "exists")
			}
			assertServerFailure(t, l, c.ID, "grounding", c.Round, checks...)
			if offered, err := tryCheckApproval(t, l, c.ID, inbox, src); offered {
				t.Errorf("approval should not be offered after a failed server check; Decide returned %v", err)
			}
		})
	}
}

func TestKeylessGroundingRedoRetainsPriorFailure(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	src := &tracedReviewSources{retracted: map[string]bool{paperB: true}}
	l.e.Sources = src
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	captureChecks(t, l, c.ID, "first round", src)
	assertServerFailure(t, l, c.ID, "grounding", c.Round, "not_retracted")
	got := l.card(t, c.ID)
	if got.Round == c.Round && got.State == "blocked" {
		l.e.OnComment(l.ws, c.ID, l.owner, "Reassess the paper evidence.")
	}
	got = l.card(t, c.ID)
	captureChecks(t, l, c.ID, "next round started", src)
	if got.Round != c.Round+1 || got.BoardColumn != "grounding" || got.State != "working" {
		t.Fatalf("expected a new grounding round, got %s/%s round %d", got.BoardColumn, got.State, got.Round)
	}
	if !strings.Contains(got.HandoffNote, paperB) {
		t.Errorf("expected failing paper %s in redo handoff, got %q", paperB, got.HandoffNote)
	}
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "next round submitted without withdrawing the bad paper", src)
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) < 2 || !slices.ContainsFunc(rs[len(rs)-1].Items, func(i review.Item) bool {
		return i.Label == paperB && i.Status == review.Fail
	}) {
		t.Error("expected the unanswered failure to remain in the next review")
	}
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, src); offered {
		t.Errorf("approval should not be offered with the prior failure unanswered; Decide returned %v", err)
	}
}

func TestKeylessHypothesesRecordInvalidCitation(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	ground := l.groundingPaper(t, c.ID, paperA)
	for i, cite := range []string{"r9999", ground} {
		l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{
			Text: fmt.Sprintf("Hypothesis %d about measurement.", i+1), Prediction: review.Prediction{Shape: "direction", Direction: "decrease"},
			Control: review.Control{Text: "Standard control"}, Cites: []string{cite},
		})
	}
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before approval", nil)
	assertServerFailure(t, l, c.ID, "hypotheses", c.Round, "cites_recorded")
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, nil); offered {
		t.Errorf("approval should not be offered for an invalid citation; Decide returned %v", err)
	}
}

func checkedScript(t *testing.T, l *lab, c db.Card) string {
	t.Helper()
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{
			WorkspaceID: l.ws, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round,
			Kind: "run", Role: "server", Key: "x1",
			Body: []byte(`{"tool":"run_script","output":"Effect: 0.12\n","exit_status":0}`),
		})
		return err
	})
	return "x1"
}

func assertScriptMatches(t *testing.T, l *lab, c db.Card, want map[string]bool) {
	t.Helper()
	ctx := context.Background()
	rv, err := l.e.readReview(ctx, l.ws, c)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := LoadRuns(ctx, l.e.Blobs, rv.runs)
	if err != nil {
		t.Fatal(err)
	}
	matches := MatchValues(rv.work, runs)
	if len(matches) != len(want) {
		t.Fatalf("fixture: expected %d value checks; got %+v", len(want), matches)
	}
	for _, m := range matches {
		matched, known := want[m.Value]
		if !known || m.Matched != matched || m.Uncited {
			t.Fatalf("fixture: expected match states %v; got %+v", want, matches)
		}
	}
	t.Logf("deterministic script value matches: %+v", matches)
}

func TestKeylessExperimentationRecordsMissingOutput(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "experimentation", "validation", a)
	l.e.Kick(l.ws, c.ID)
	run := checkedScript(t, l, c)
	l.item(t, c.ID, "result", map[string]any{"text": "The effect is 0.12.", "cites": []string{"run:" + run}, "value": 0.12})
	l.item(t, c.ID, "result", map[string]any{"text": "The effect is 0.99.", "cites": []string{"run:" + run}, "value": 0.99})
	l.item(t, c.ID, review.KindOutputs, review.OutputsBody{Files: []string{"missing.csv"}})
	assertScriptMatches(t, l, c, map[string]bool{"0.12": true, "0.99": false})
	l.done(t, c.ID)
	l.e.Kick(l.ws, c.ID)
	inbox := captureChecks(t, l, c.ID, "before approval", nil)
	assertServerFailure(t, l, c.ID, "experimentation", c.Round, "file:missing.csv", "traced")
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, nil); offered {
		t.Errorf("approval should not be offered for a missing output; Decide returned %v", err)
	}
}

func TestKeylessValidationClaimsRecordNonverbatimQuote(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.e.Blobs = storage.Local{Dir: t.TempDir()}
	c := l.study(t, "validation", true, nil)
	ctx := context.Background()
	data := "group,auc\ntreated,0.873\n"
	blob := l.ws.String() + "/outputs/results"
	if err := l.e.Blobs.Put(ctx, blob, strings.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{
			WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "experimentation", Round: 1,
			Kind: "output", Role: "leader", Key: "o1", BlobKey: blob,
			Body: []byte(fmt.Sprintf(`{"name":"results.csv","size_bytes":%d,"content_type":"text/csv"}`, len(data))),
		})
		return err
	})
	l.e.Kick(l.ws, c.ID)
	for i, quote := range []string{"treated,0.919", "treated,0.873"} {
		l.item(t, c.ID, review.KindVerdict, review.VerdictBody{
			Claim: fmt.Sprintf("Claim %d about the AUC.", i+1), Verdict: "supported", Source: "results.csv", Quote: quote,
		})
	}
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before approval", nil)
	assertServerFailure(t, l, c.ID, "validation", c.Round, "quote_in_source")
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, nil); offered {
		t.Errorf("approval should not be offered for a nonverbatim quote; Decide returned %v", err)
	}
}

func TestKeylessValidationReplayInvaliditySurvivesObjections(t *testing.T) {
	for _, withObjection := range []bool{false, true} {
		t.Run(fmt.Sprintf("objection_%t", withObjection), func(t *testing.T) {
			l := newLab(t)
			l.e.Jev = nil
			reproduced := false
			c := modeAStudy(t, l, &reproduced)
			l.assess(t, c.ID)
			before := captureChecks(t, l, c.ID, "before objector settles", nil)
			if len(before) != 0 {
				t.Error("expected no decision until the objector settles")
			}
			if statuses := l.statuses(t, c.ID, false); len(statuses) != 1 || statuses[0].Status != review.Invalid {
				t.Errorf("expected preliminary invalid replay status; got %+v", statuses)
			}
			if withObjection {
				l.object(t, c.ID, review.Objection{Label: "Rival Q", Type: "leakage", Problem: "Unknown target.", Checks: []review.Check{}})
			} else {
				l.object(t, c.ID)
			}
			inbox := captureChecks(t, l, c.ID, "after objector settles", nil)
			if withObjection {
				judged := l.judgedObjections(t, c.ID)
				if len(judged) != 1 || !strings.Contains(judged[0].Server, "not one the objector was shown") {
					t.Errorf("expected recorded server rejection of unknown objection target; got %+v", judged)
				}
			}
			rs := l.reviews(t, c.ID, "validation")
			if len(rs) != 1 || rs[0].Decision != decideRouteBack || l.card(t, c.ID).BoardColumn != "experimentation" {
				t.Errorf("invalid replay should route back to Experimentation; reviews=%d", len(rs))
			}
			if offered, err := tryCheckApproval(t, l, c.ID, inbox, nil); offered {
				t.Errorf("approval should not be offered with an invalid replay; Decide returned %v", err)
			}
			for _, message := range l.inbox(t, c.ID, "needs_decision") {
				if strings.Contains(message, "passed the server's checks") {
					t.Errorf("misleading inbox text: %q", message)
				}
			}
		})
	}
}

func TestKeylessLegacyScoringDoesNotClaimFailedValuePassed(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.legacy = true
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "experimentation", "validation", a)
	l.e.Kick(l.ws, c.ID)
	run := checkedScript(t, l, c)
	result := l.item(t, c.ID, "result", map[string]any{"text": "The effect is 0.99.", "cites": []string{"run:" + run}, "value": 0.99})
	assertScriptMatches(t, l, c, map[string]bool{"0.99": false})
	l.done(t, c.ID, result)
	inbox := captureChecks(t, l, c.ID, "before approval", nil)
	for _, message := range l.inbox(t, c.ID, "needs_decision") {
		if strings.Contains(message, "passed the server's checks") {
			t.Errorf("misleading inbox text: %q", message)
		}
	}
	if messages := l.inbox(t, c.ID, "needs_decision"); len(messages) != 1 || !strings.Contains(messages[0], `value "0.99" did not match a successful cited script run`) {
		t.Errorf("the decision must disclose the unmatched value: %q", messages)
	}
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, nil); !offered || err != nil || l.card(t, c.ID).BoardColumn != "validation" {
		t.Errorf("shown legacy mismatch should remain approvable; offered=%t error=%v", offered, err)
	}
}

func approvalClock(l *lab) func(time.Duration) {
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	l.e.Now = func() time.Time { return now }
	return func(d time.Duration) { now = now.Add(d) }
}

func TestKeylessApprovalRechecksRetraction(t *testing.T) {
	// Approval trusts a fresh snapshot. A retraction is only observed when
	// a stale snapshot sends the study through background review again.
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_%t", expired), func(t *testing.T) {
			l := newLab(t)
			advanceTime := approvalClock(l)
			l.e.Jev = nil
			src := &tracedReviewSources{retracted: map[string]bool{}}
			l.e.Sources = src
			c := l.groundingStudy(t)
			l.item(t, c.ID, review.KindPaper, paper(paperA))
			l.done(t, c.ID)
			inbox := captureChecks(t, l, c.ID, "before retraction", src)
			src.retracted[paperA] = true
			before := len(src.calls)
			if expired {
				advanceTime(approvalChecksMaxAge)
			}
			offered, err := tryCheckApproval(t, l, c.ID, inbox, src)
			if len(src.calls) != before {
				t.Errorf("approval made live calls: %v", src.calls[before:])
			}
			if !expired {
				if !offered || err != nil || l.card(t, c.ID).BoardColumn != "hypotheses" {
					t.Errorf("fresh snapshot should advance despite an unseen upstream retraction; offered=%t error=%v", offered, err)
				}
				return
			}
			if !offered || err == nil || !strings.Contains(err.Error(), "being refreshed") {
				t.Fatalf("stale approval should request refresh; offered=%t error=%v", offered, err)
			}
			l.e.Kick(l.ws, c.ID)
			got := l.card(t, c.ID)
			if got.BoardColumn != "grounding" || got.Round != c.Round+1 || got.Redos != 1 {
				t.Errorf("refresh should catch the retraction and redo, got %s/%s round %d redos %d", got.BoardColumn, got.State, got.Round, got.Redos)
			}
			assertServerFailure(t, l, c.ID, "grounding", c.Round, "not_retracted")
		})
	}
}

func TestUnreadableOwnerKeyRetriesReview(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.e.JevKey = func(context.Context, uuid.UUID) (string, error) { return "", fmt.Errorf("sealed key unavailable") }
	l.e.Sources = &tracedReviewSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	if got := l.card(t, c.ID); got.State != "working" || got.Round != c.Round {
		t.Errorf("expected retry in the same working round, got %s/%s round %d", got.BoardColumn, got.State, got.Round)
	}
	if messages := l.inbox(t, c.ID, "scoring_unavailable"); len(messages) != 1 || !strings.Contains(messages[0], "could not read") {
		t.Errorf("expected an unreadable-key retry explanation, got %q", messages)
	}
	if messages := l.inbox(t, c.ID, "needs_decision"); len(messages) != 0 {
		t.Errorf("unexpected approval decision: %q", messages)
	}
}

func TestKeylessUnscreenedRecallAloneCannotHoldGrounding(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.e.Sources = &tracedReviewSources{recall: []sources.Hit{{ID: paperA, Title: "Unscreened candidate"}}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindNoQualifying, review.NoQualifyingBody{Text: "No source met the criterion."})
	l.done(t, c.ID)
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 1 || rs[0].Recall == nil || rs[0].Recall.Screened != 0 || !slices.Equal(rs[0].Recall.Unscreened, []string{paperA}) || len(rs[0].Missed) != 0 {
		t.Fatalf("expected a recorded unscreened candidate, got %+v", rs)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "hypotheses" || got.State != "working" {
		t.Errorf("recall alone held the study: %s/%s", got.BoardColumn, got.State)
	}
}

func TestKeylessAllWithdrawnRequiresNonApprovalDecision(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.e.Sources = stubSources{missing: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	key := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.withdraw(t, c.ID, key)
	l.done(t, c.ID)
	rs := l.reviews(t, c.ID, "grounding")
	if len(rs) != 2 || rs[0].Decision != review.DecideRedo || rs[1].Decision != review.DecideAsk || !review.AllWithdrawn(rs[1]) {
		t.Fatalf("expected redo followed by all-withdrawn decision, got %+v", rs)
	}
	l.wantStopDecision(t, c.ID)
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND cause='all_withdrawn' AND resolved_at IS NULL", c.ID); n != 1 {
		t.Fatalf("expected one all-withdrawn decision, got %d", n)
	}
}

func TestKeyedRetryKeepsDeterministicChecks(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 503, levels(nil))
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	if got := l.card(t, c.ID); got.State != "working" || got.Round != c.Round {
		t.Fatalf("expected keyed retry in the same round, got %s round %d", got.State, got.Round)
	}
	if n := l.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='server_checks'
		AND body->'items'->0->'checks'->0->>'id'='exists'`, c.ID); n != 1 {
		t.Fatalf("expected persisted deterministic findings during outage, got %d", n)
	}
	if n := len(l.inbox(t, c.ID, "needs_decision")); n != 0 {
		t.Fatalf("expected no owner approval, got %d", n)
	}
	stub.mu.Lock()
	stub.status = 200
	stub.mu.Unlock()
	stub.setNoul(func(string) float64 {
		if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='server_checks'", c.ID); n == 0 {
			t.Error("Jev was called before the server checks were persisted")
		}
		return 1
	})
	l.e.Kick(l.ws, c.ID)
	if got := l.card(t, c.ID); got.BoardColumn != "hypotheses" || got.Round != c.Round+1 {
		t.Errorf("expected advance after the retry, got %s round %d", got.BoardColumn, got.Round)
	}
}

func TestKeylessApprovalRechecksCarriedPaper(t *testing.T) {
	l := newLab(t)
	// A refresh must check the source behind a carried judgment as well.
	advanceTime := approvalClock(l)
	stub := l.jev(t, 200, levels(nil))
	stub.setNoul(func(string) float64 { return 1 })
	src := &tracedReviewSources{
		stubSources: stubSources{missing: map[string]bool{paperB: true}},
		retracted:   map[string]bool{},
	}
	l.e.Sources = src
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	bad := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.e.Jev = nil
	l.withdraw(t, c.ID, bad)
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.done(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before carried retraction", src)
	src.retracted[paperA] = true
	advanceTime(approvalChecksMaxAge)
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, src); !offered || err == nil || !strings.Contains(err.Error(), "being refreshed") {
		t.Errorf("approval should reject a newly retracted carried paper; offered=%t error=%v", offered, err)
	}
	l.e.Kick(l.ws, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "grounding" || got.State != "blocked" || got.BlockKind != "low_score" {
		t.Errorf("expected to block after the spent redo, got %s/%s (%s)", got.BoardColumn, got.State, got.BlockKind)
	}
	assertServerFailure(t, l, c.ID, "grounding", c.Round+1, "not_retracted")
}

func TestOpenKeylessDecisionUsesRetryIfKeyCannotBeRead(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before adding key", nil)
	l.e.JevKey = func(context.Context, uuid.UUID) (string, error) { return "", fmt.Errorf("sealed key unavailable") }
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, nil); !offered || err == nil {
		t.Errorf("expected a retry when the newly saved key cannot be read; offered=%t error=%v", offered, err)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "grounding" || got.State != "working" || got.Round != c.Round {
		t.Errorf("expected retry in the original round, got %s/%s round=%d", got.BoardColumn, got.State, got.Round)
	}
	if messages := l.inbox(t, c.ID, "scoring_unavailable"); len(messages) != 1 || !strings.Contains(messages[0], "could not read") {
		t.Errorf("missing unreadable-key notice: %q", messages)
	}
}

func TestKeylessRedoKeepsUnjudgedStandingPaper(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.e.Sources = &tracedReviewSources{retracted: map[string]bool{paperB: true}}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	bad := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	stub := l.jev(t, 200, levels(nil))
	stub.setNoul(func(string) float64 { return 1 })
	l.withdraw(t, c.ID, bad)
	l.done(t, c.ID)
	calls := nouls(stub.calls())
	if len(calls) != 1 || !strings.Contains(calls[0].State, paperA) {
		t.Fatalf("expected Jev to judge the standing unjudged paper on redo, got %+v", calls)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "hypotheses" {
		t.Errorf("expected the corrected redo to advance, got %s/%s", got.BoardColumn, got.State)
	}
}

func TestKeylessRecallOutagePreservesKnownFailure(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	src := &tracedReviewSources{retracted: map[string]bool{paperB: true}, searchErr: fmt.Errorf("recall unavailable")}
	l.e.Sources = src
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	// The failure is durable, but a temporary recall error must not spend
	// the redo. Only after recall recovers do we act on the retraction.
	assertReviewWaiting(t, l, c, "recall search")
	src.searchErr = nil
	l.e.Kick(l.ws, c.ID)
	assertServerFailure(t, l, c.ID, "grounding", c.Round, "not_retracted")
	if got := l.card(t, c.ID); got.Round != c.Round+1 || got.Redos != 1 {
		t.Errorf("expected the redo after recall recovery, got round=%d redos=%d", got.Round, got.Redos)
	}
}

func TestKeylessApprovalRejectsChangedEvidence(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	src := &tracedReviewSources{}
	l.e.Sources = src
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before evidence changes", src)
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	before := len(src.calls)
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, src); !offered || err == nil || !strings.Contains(err.Error(), "being refreshed") {
		t.Errorf("expected changed evidence to require a refresh; offered=%t error=%v", offered, err)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "grounding" || got.Round != c.Round || len(src.calls) != before {
		t.Errorf("changed evidence advanced or triggered a live check: %s round %d calls=%v", got.BoardColumn, got.Round, src.calls[before:])
	}
}

func TestKeylessApprovalChecksEvidenceAgainInCommitTransaction(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.e.Sources = stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before new guidance", nil)
	if len(inbox) != 1 {
		t.Fatalf("expected one approval, got %d", len(inbox))
	}
	current := l.card(t, c.ID)
	checked := approvalChecks{checkedAt: l.e.now(), version: current.Version}
	l.in(t, func(tx *store.Tx) (err error) {
		checked.evidence, err = approvalEvidence(t.Context(), tx, c.ID)
		return err
	})
	// Model an input committed between the first decision read and the
	// final transaction, before its asynchronous wake can bump the card.
	if _, err := l.env.Owner.Exec(t.Context(), "INSERT INTO comment(workspace_id,card_id,author_id,body) VALUES ($1,$2,$3,$4)",
		l.ws, c.ID, l.owner, "Check a new population before advancing."); err != nil {
		t.Fatal(err)
	}
	err := l.e.decide(t.Context(), l.ws, inbox[0].ID, l.owner, "approve", "", &checked)
	if err == nil || !strings.Contains(err.Error(), "being refreshed") || l.card(t, c.ID).BoardColumn != "grounding" {
		t.Errorf("approval accepted changed evidence in its final transaction: %v", err)
	}
}

func TestKeylessReplayApprovalFinalizesCodeStatuses(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	reproduced := true
	c := modeAStudy(t, l, &reproduced)
	l.assess(t, c.ID)
	l.object(t, c.ID, review.Objection{Label: "Rival A", Type: "overreach", Problem: "One cohort.", Checks: []review.Check{}})
	inbox := captureChecks(t, l, c.ID, "before approval", nil)
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, nil); !offered || err != nil {
		t.Fatalf("expected owner approval on valid replay; offered=%t error=%v", offered, err)
	}
	if final := l.statuses(t, c.ID, true); len(final) != 1 || final[0].Status != review.Supported {
		t.Errorf("expected final server findings from approved replay, got %+v", final)
	}
	if judged := l.judgedObjections(t, c.ID); len(judged) != 0 {
		t.Errorf("owner approval should not manufacture an objection judgment: %+v", judged)
	}
}

func TestLegacyRecheckReplacesDecisionWhenDiagnosticsChange(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	l.legacy = true
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "experimentation", "validation", a)
	l.e.Kick(l.ws, c.ID)
	run := checkedScript(t, l, c)
	result := l.item(t, c.ID, "result", map[string]any{"text": "The effect is 0.12.", "cites": []string{"run:" + run}, "value": 0.12})
	l.done(t, c.ID, result)
	initial := captureChecks(t, l, c.ID, "before script evidence changed", nil)
	_, err := l.env.Owner.Exec(t.Context(), `UPDATE record SET body=jsonb_set(body,'{output}',to_jsonb('Effect: 0.45'::text)) WHERE card_id=$1 AND key='x1'`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if offered, err := tryCheckApproval(t, l, c.ID, initial, nil); !offered || err == nil {
		t.Fatalf("approval should refresh the mismatch decision; offered=%t error=%v", offered, err)
	}
	if offered, err := tryCheckApproval(t, l, c.ID, initial, nil); !offered || err == nil {
		t.Fatalf("the old decision must stay closed; offered=%t error=%v", offered, err)
	}
	l.e.Kick(l.ws, c.ID)
	updated := captureChecks(t, l, c.ID, "updated diagnostics", nil)
	if offered, err := tryCheckApproval(t, l, c.ID, updated, nil); !offered || err != nil {
		t.Errorf("expected approval after seeing updated diagnostics; offered=%t error=%v", offered, err)
	}
}

func assertReviewWaiting(t *testing.T, l *lab, c db.Card, notice string) {
	t.Helper()
	got := l.card(t, c.ID)
	if got.BoardColumn != c.BoardColumn || got.State != "working" || got.Round != c.Round || got.Redos != 0 {
		t.Errorf("temporary outage spent the redo or changed stage: %s/%s round=%d redos=%d", got.BoardColumn, got.State, got.Round, got.Redos)
	}
	if messages := l.inbox(t, c.ID, "scoring_unavailable"); len(messages) != 1 || !strings.Contains(messages[0], notice) {
		t.Errorf("expected one %q retry notice, got %q", notice, messages)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND kind='needs_decision' AND resolved_at IS NULL", c.ID); n != 0 {
		t.Errorf("outage left %d open owner decisions", n)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='server_checks' AND body->'items' @> '[{\"checks\":[{\"id\":\"not_retracted\",\"status\":\"fail\"}]}]'::jsonb", c.ID); n == 0 {
		t.Error("the known retraction was not persisted while waiting")
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='outcome' AND round=$2", c.ID, c.Round); n != 0 {
		t.Errorf("outage created %d outcomes before judgment recovered", n)
	}
}

func TestMixedGroundingOutageKeepsRedoForCompleteReview(t *testing.T) {
	for _, tc := range []struct{ name, notice string }{
		{"typesafe_503", "TypeSafe"},
		{"unreadable_key", "could not read your saved TypeSafe key"},
		{"source_lookup", "source lookup"},
		{"recall", "recall search"},
	} {
		for _, order := range []struct {
			name   string
			papers []string
		}{
			{"good_first", []string{paperA, paperB}},
			{"retracted_first", []string{paperB, paperA}},
		} {
			t.Run(tc.name+"/"+order.name, func(t *testing.T) {
				l := newLab(t)
				stub := l.jev(t, 200, levels(nil))
				stub.setNoul(func(string) float64 { return 0 })
				j := l.e.Jev
				src := &tracedReviewSources{retracted: map[string]bool{paperB: true}}
				l.e.Sources = src
				switch tc.name {
				case "typesafe_503":
					stub.status = 503
				case "unreadable_key":
					l.e.Jev = nil
					l.e.JevKey = func(context.Context, uuid.UUID) (string, error) { return "", errors.New("could not open fixture key") }
				case "source_lookup":
					src.paperErrors = map[string]error{paperA: errors.New("lookup offline")}
				case "recall":
					src.searchErr = errors.New("recall offline")
				}
				c := l.groundingStudy(t)
				for _, id := range order.papers {
					l.item(t, c.ID, review.KindPaper, paper(id))
				}
				l.done(t, c.ID)
				assertReviewWaiting(t, l, c, tc.notice)
				stub.mu.Lock()
				stub.status = 200
				stub.mu.Unlock()
				l.e.Jev = j
				src.paperErrors, src.searchErr = nil, nil
				l.e.Kick(l.ws, c.ID)
				got := l.card(t, c.ID)
				if got.State != "working" || got.Round != c.Round+1 || got.Redos != 1 ||
					!strings.Contains(got.HandoffNote, paperA+":") ||
					!strings.Contains(got.HandoffNote, paperB+": not_retracted: marked retracted") {
					t.Errorf("recovery should produce one redo with both failures; got %s round=%d redos=%d handoff=%q", got.State, got.Round, got.Redos, got.HandoffNote)
				}
				if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='outcome' AND body->>'verdict'='redo'", c.ID); n != 1 {
					t.Errorf("expected one redo outcome, got %d", n)
				}
			})
		}
	}
}

func TestApprovalOutageKeepsRedoAndWithdrawsApproval(t *testing.T) {
	l := newLab(t)
	advanceTime := approvalClock(l)
	l.e.Jev = nil
	src := &tracedReviewSources{}
	l.e.Sources = src
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before evidence changes", src)
	src.retracted = map[string]bool{paperB: true}
	src.paperErrors = map[string]error{paperA: errors.New("lookup offline")}
	advanceTime(approvalChecksMaxAge)
	if offered, err := tryCheckApproval(t, l, c.ID, inbox, src); !offered || err == nil {
		t.Fatalf("expected a rejected approval; offered=%t err=%v", offered, err)
	}
	l.e.Kick(l.ws, c.ID)
	assertReviewWaiting(t, l, c, "source lookup")
	src.paperErrors = nil
	l.e.Kick(l.ws, c.ID)
	got := l.card(t, c.ID)
	if got.Round != c.Round+1 || got.Redos != 1 || !strings.Contains(got.HandoffNote, paperB+": not_retracted") {
		t.Errorf("keyless recovery should redo immediately on the retraction, got round=%d redos=%d handoff=%q", got.Round, got.Redos, got.HandoffNote)
	}
}

func TestInvalidReplayRoutesBackWithoutReadingJevKey(t *testing.T) {
	l := newLab(t)
	l.e.Jev = nil
	keyReads := 0
	l.e.JevKey = func(context.Context, uuid.UUID) (string, error) {
		keyReads++
		return "", errors.New("unreadable saved key")
	}
	reproduced := false
	c := modeAStudy(t, l, &reproduced)
	l.assess(t, c.ID)
	l.object(t, c.ID, review.Objection{Label: "Rival A", Type: "overreach", Problem: "One cohort.", Checks: []review.Check{}})
	got := l.card(t, c.ID)
	if got.BoardColumn != "experimentation" || got.Round != c.Round+1 || keyReads != 0 {
		t.Errorf("expected final server invalidity to route back without Jev; stage=%s round=%d key reads=%d", got.BoardColumn, got.Round, keyReads)
	}
}

func TestConcurrentKeylessApprovalsHaveOneOutcome(t *testing.T) {
	l := newLab(t)
	l.e.Jev, l.e.Sources = nil, stubSources{}
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	l.done(t, c.ID)
	inbox := captureChecks(t, l, c.ID, "before concurrent approval", nil)
	if len(inbox) != 1 {
		t.Fatalf("expected exactly one decision, got %d", len(inbox))
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- l.e.Decide(t.Context(), l.ws, inbox[0].ID, l.owner, "approve", "")
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a != nil {
		a, b = b, a
	}
	if a != nil || !errors.Is(b, ErrDecisionClosed) && !errors.Is(b, ErrNotWaiting) {
		t.Errorf("expected one success and one conflict, got %v and %v", a, b)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='outcome' AND body->>'verdict'='approved_by_owner'", c.ID); n != 1 {
		t.Errorf("expected one owner outcome, got %d", n)
	}
}

// A short request deadline stands in for the old approach's per-request
// budget. The fixture still uses the production arXiv pacing in review.
type approvalPacedSources struct {
	stubSources
	papers, searches int
	paced            bool
}

func (s *approvalPacedSources) Paper(ctx context.Context, id string) (sources.Hit, error) {
	s.papers++
	if s.paced {
		select {
		case <-ctx.Done():
			return sources.Hit{}, ctx.Err()
		case <-time.After(sources.ArxivGap):
		}
	}
	return s.stubSources.Paper(ctx, id)
}

func (s *approvalPacedSources) Search(context.Context, string) ([]sources.Hit, error) {
	s.searches++
	return nil, nil
}

func TestKeylessApprovalUsesPersistedChecksForPacedSources(t *testing.T) {
	for _, n := range []int{1, 4, 12} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			l := newLab(t)
			src := &approvalPacedSources{}
			l.e.Jev, l.e.Sources = nil, src
			c := l.groundingStudy(t)
			for i := range n {
				l.item(t, c.ID, review.KindPaper, paper(fmt.Sprintf("arxiv:2101.%05d", i+1)))
			}
			l.done(t, c.ID)
			if src.papers != n || src.searches == 0 {
				t.Fatalf("background review checked %d papers and ran %d searches", src.papers, src.searches)
			}
			inbox := captureChecks(t, l, c.ID, "before approval", nil)
			before := src.papers + src.searches
			src.paced = true
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if len(inbox) != 1 {
				t.Fatalf("expected exactly one approval, got %d", len(inbox))
			}
			if err := l.e.Decide(ctx, l.ws, inbox[0].ID, l.owner, "approve", ""); err != nil {
				t.Errorf("fresh decision should be immediately approvable: %v", err)
			}
			if delta := src.papers + src.searches - before; delta != 0 {
				t.Errorf("approval made %d live sources calls", delta)
			}
			if l.card(t, c.ID).BoardColumn != "hypotheses" {
				t.Error("approval did not advance the study")
			}
		})
	}
}

func TestKeylessApprovalRefreshesUnusableSnapshotOnce(t *testing.T) {
	for _, reason := range []string{"expired", "pre_fix", "incomplete", "wrong_round", "wrong_column"} {
		t.Run(reason, func(t *testing.T) {
			l := newLab(t)
			advanceTime := approvalClock(l)
			src := &tracedReviewSources{}
			l.e.Jev, l.e.Sources = nil, src
			c := l.groundingStudy(t)
			l.item(t, c.ID, review.KindPaper, paper(paperA))
			l.done(t, c.ID)
			initial := captureChecks(t, l, c.ID, "before forcing refresh", src)
			if reason == "expired" {
				advanceTime(approvalChecksMaxAge)
			} else {
				update := map[string]string{
					"pre_fix":      "body = body - 'approval_decision' - 'checked_at' - 'evidence_revision'",
					"incomplete":   "body = jsonb_set(body, '{complete}', 'false')",
					"wrong_round":  "round = round + 1",
					"wrong_column": "board_column = 'hypotheses'",
				}[reason]
				if _, err := l.env.Owner.Exec(t.Context(), "UPDATE record SET "+update+
					" WHERE card_id=$1 AND kind='server_checks' AND body ? 'approval_decision'", c.ID); err != nil {
					t.Fatal(err)
				}
			}
			calls := len(src.calls)
			if offered, err := tryCheckApproval(t, l, c.ID, initial, src); !offered || err == nil || !strings.Contains(err.Error(), "being refreshed") {
				t.Fatalf("unusable check snapshot should schedule a refresh; offered=%t error=%v", offered, err)
			}
			if got := l.card(t, c.ID); got.State != "working" || got.Round != c.Round || got.Redos != 0 || len(src.calls) != calls {
				t.Fatalf("request spent a redo or checked sources: %+v, calls %v", got, src.calls[calls:])
			}
			l.e.Kick(l.ws, c.ID)
			fresh := captureChecks(t, l, c.ID, "after background refresh", src)
			if len(fresh) != 2 || fresh[1].ID == initial[0].ID || fresh[1].Status != "open" {
				t.Fatalf("expected exactly one fresh decision, got %+v", fresh)
			}
			calls = len(src.calls)
			if offered, err := tryCheckApproval(t, l, c.ID, fresh, src); !offered || err != nil || len(src.calls) != calls {
				t.Fatalf("fresh decision should be immediately approvable with no sources calls; offered=%t err=%v calls=%v", offered, err, src.calls[calls:])
			}
		})
	}
}

func TestFirstKeylessApprovalSnapshotChecksCarriedSources(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	stub.setNoul(func(string) float64 { return 1 })
	src := &tracedReviewSources{
		stubSources: stubSources{missing: map[string]bool{paperB: true}},
		retracted:   map[string]bool{},
	}
	l.e.Sources = src
	c := l.groundingStudy(t)
	l.item(t, c.ID, review.KindPaper, paper(paperA))
	bad := l.item(t, c.ID, review.KindPaper, paper(paperB))
	l.done(t, c.ID)
	l.e.Jev = nil
	src.retracted[paperA] = true
	l.withdraw(t, c.ID, bad)
	l.item(t, c.ID, review.KindPaper, paper(paperB2))
	l.done(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "grounding" || got.State != "blocked" || got.BlockKind != "low_score" {
		t.Errorf("expected the first keyless pass to block on the carried retraction; got %s/%s (%s)", got.BoardColumn, got.State, got.BlockKind)
	}
	assertServerFailure(t, l, c.ID, "grounding", c.Round+1, "not_retracted")
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND cause='no_jev' AND resolved_at IS NULL", c.ID); n != 0 {
		t.Errorf("the stale carried paper produced %d approval decisions", n)
	}
}
