package gate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
)

// fakeJev answers every question with p for "yes"; fail decides, per call
// (counted from 1) and per question text, whether to answer 503 instead.
func fakeJev(t *testing.T, p float64, fail func(n int32, instructions string) bool) *jev.Client {
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]struct{ Instructions string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if fail != nil && fail(n.Add(1), body.Questions["q"].Instructions) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": map[string]any{
			"q": map[string]any{"choice": "yes", "probabilities": map[string]float64{"yes": p, "no": 1 - p}}}})
	}))
	t.Cleanup(s.Close)
	return &jev.Client{BaseURL: s.URL, Model: jev.Model, HTTP: s.Client()}
}

func question(id string) Question {
	return Question{ID: id, State: "passage: x", Instructions: "Does " + id + " hold?",
		Options: map[string]string{"yes": "It holds", "no": "It does not"}, Pass: "yes"}
}

// fakeRetry is NewRetry's budget with a sleep that only records its waits.
func fakeRetry(slept *[]time.Duration) *Retry {
	r := NewRetry()
	r.Sleep = func(_ context.Context, d time.Duration) error { *slept = append(*slept, d); return nil }
	return r
}

func TestAskPass(t *testing.T) {
	var slept []time.Duration
	ch, c := Ask(context.Background(), fakeJev(t, 0.9, nil), question("a"), board.DefaultBands, fakeRetry(&slept))
	if ch.Result != board.Pass || ch.Judge != board.Jev || ch.P == nil || *ch.P != 0.9 || ch.Choice != "yes" || ch.Model != jev.Model {
		t.Fatalf("check %+v", ch)
	}
	if c.Judge != "jev" || c.Answer != "yes" || c.P["yes"] != 0.9 || c.Model != jev.Model || c.Question != "a" || c.InputHash == "" || len(slept) != 0 {
		t.Fatalf("choice %+v slept %v", c, slept)
	}
}

func TestAskBandsFromPack(t *testing.T) {
	ch, _ := Ask(context.Background(), fakeJev(t, 0.8, nil), question("a"), board.Bands{Yes: 0.9, No: 0.5}, &Retry{})
	if ch.Result != board.Attention {
		t.Fatalf("pack bands ignored: %+v", ch)
	}
	if ch, _ = Ask(context.Background(), fakeJev(t, 0.8, nil), question("a"), board.DefaultBands, &Retry{}); ch.Result != board.Pass {
		t.Fatalf("default bands: %+v", ch)
	}
}

func TestAskRetriesThenFallback(t *testing.T) {
	var slept []time.Duration
	ch, c := Ask(context.Background(), fakeJev(t, 0.9, func(n int32, _ string) bool { return n <= 2 }), question("a"), board.DefaultBands, fakeRetry(&slept))
	if ch.Result != board.Pass || c.Judge != "jev" || len(slept) != 2 {
		t.Fatalf("check %+v choice %+v slept %v", ch, c, slept)
	}
	slept = nil
	ch, c = Ask(context.Background(), fakeJev(t, 0.9, func(int32, string) bool { return true }), question("a"), board.DefaultBands, fakeRetry(&slept))
	if ch.Result != board.Attention || ch.Judge != board.Reviewer || ch.P != nil || c.Judge != "fallback" || c.Answer != "" || !strings.HasPrefix(ch.Note, "jev unavailable after 3 attempts: ") {
		t.Fatalf("check %+v choice %+v", ch, c)
	}
	if !reflect.DeepEqual(slept, []time.Duration{3 * time.Minute, 3 * time.Minute}) {
		t.Fatalf("slept %v", slept)
	}
}

func TestAskOff(t *testing.T) {
	ch, c := Ask(context.Background(), nil, question("a"), board.DefaultBands, NewRetry())
	if ch.Result != board.Attention || ch.Judge != board.Reviewer || ch.Note != "jev egress is off: the reviewer answers" || c.Judge != "fallback" || c.Candidates["yes"] == "" {
		t.Fatalf("check %+v choice %+v", ch, c)
	}
}

func TestAskPartialOutage(t *testing.T) {
	var slept []time.Duration
	j := fakeJev(t, 0.9, func(_ int32, instructions string) bool { return strings.Contains(instructions, "q2") })
	r := fakeRetry(&slept)
	var judges []string
	var results []board.Result
	g := &board.Gate{Column: board.Grounding}
	for _, id := range []string{"q1", "q2", "q3"} {
		ch, c := Ask(context.Background(), j, question(id), board.DefaultBands, r)
		judges = append(judges, c.Judge)
		results = append(results, ch.Result)
		g.Checks = append(g.Checks, ch)
	}
	if !reflect.DeepEqual(judges, []string{"jev", "fallback", "jev"}) {
		t.Fatalf("judges %v", judges)
	}
	// The stand-in's check waits for the reviewer: until it is settled the
	// gate cannot pass.
	if !reflect.DeepEqual(results, []board.Result{board.Pass, board.Attention, board.Pass}) {
		t.Fatalf("results %v", results)
	}
	board.Decide(g)
	if g.Verdict != board.VFail || !strings.HasPrefix(g.Reason, "q2:") {
		t.Fatalf("an unsettled fallback decided the gate: %s %q", g.Verdict, g.Reason)
	}
}

func TestRetryBudgetShared(t *testing.T) {
	var slept []time.Duration
	j := fakeJev(t, 0.9, func(int32, string) bool { return true })
	r := fakeRetry(&slept)
	if _, c := Ask(context.Background(), j, question("a"), board.DefaultBands, r); c.Judge != "fallback" || len(slept) != 2 {
		t.Fatalf("first: %+v slept %v", c, slept)
	}
	slept = nil
	if _, c := Ask(context.Background(), j, question("b"), board.DefaultBands, r); c.Judge != "fallback" || len(slept) != 0 {
		t.Fatalf("second: %+v slept %v", c, slept)
	}
}

func TestInputHashStable(t *testing.T) {
	q := question("a")
	q.Options = map[string]string{}
	q2 := q
	q2.Options = map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		q.Options[k] = "option " + k
	}
	for _, k := range []string{"f", "e", "d", "c", "b", "a"} {
		q2.Options[k] = "option " + k
	}
	for range 20 {
		if inputHash(q) != inputHash(q2) {
			t.Fatal("option order changed the hash")
		}
	}
	q2.State = "passage: y"
	if inputHash(q) == inputHash(q2) {
		t.Fatal("a changed state kept the hash")
	}
	if _, c := Ask(context.Background(), nil, q, board.DefaultBands, &Retry{}); c.InputHash != inputHash(q) {
		t.Fatalf("choice hash %q", c.InputHash)
	}
}

func TestChoicePut(t *testing.T) {
	p := ChoicePut(Choice{Question: "support.e1", Judge: "jev"})
	if p.Kind != "choice" || p.Key != "support.e1" || p.Body.(Choice).Judge != "jev" {
		t.Fatalf("%+v", p)
	}
}
