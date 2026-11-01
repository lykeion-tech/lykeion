package review

import (
	"context"
	"strings"
	"testing"

	db "lykeion/server/pkg/db/generated"
)

func hyp(t *testing.T, key string, c Control, cites ...string) db.Record {
	return rec(t, key, KindHypothesis, HypothesisBody{Text: "claim " + key,
		Prediction: Prediction{Shape: "direction", Direction: "increase"}, Control: c, Cites: cites})
}

func citedPaper(t *testing.T, key string) map[string]db.Record {
	return map[string]db.Record{key: paper(t, key, "doi:10.1/a")}
}

func TestHypothesesQuestions(t *testing.T) {
	a := &fakeAsker{}
	_, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q",
		Items: []db.Record{hyp(t, "h1", Control{Text: "vehicle"}, "p1")}, Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.noul) != 1 {
		t.Fatalf("want 1 Noul call, got %d", len(a.noul))
	}
	want := map[string]string{
		"falsifiable": "Is this prediction specific enough that a result could prove it wrong?",
		"supported":   "Does the cited evidence support making this prediction?",
		"lookup":      "Does the cited evidence already contain the answer this study is testing?",
	}
	got := a.noul[0].Questions
	if len(got) != 3 {
		t.Fatalf("questions: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q", k, got[k])
		}
	}
	if !fenced(a.noul[0].State, "Research question: q") {
		t.Errorf("research question not fenced in state: %s", a.noul[0].State)
	}
	if s := a.noul[0].State; !strings.Contains(s, "claim h1") || !strings.Contains(s, "contributes doi:10.1/a") || !strings.Contains(s, "increase") {
		t.Errorf("state lacks text, prediction or evidence: %s", s)
	}
}

func TestLookupIsInverted(t *testing.T) {
	a := &fakeAsker{} // answers 0.9 to everything, lookup included
	r, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q",
		Items: []db.Record{hyp(t, "h1", Control{Text: "vehicle"}, "p1")}, Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	c := checkOf(r.Items[0], "lookup")
	if c == nil || c.Status != Fail || c.P == nil || *c.P < 0.0999 || *c.P > 0.1001 {
		t.Fatalf("lookup: %+v", c)
	}
	if r.Items[0].Status != Fail || Decide(r, 0) != DecideRedo {
		t.Errorf("item %s", r.Items[0].Status)
	}
}

func TestNoControlIsCarried(t *testing.T) {
	a := &fakeAsker{p: func(_, k string) float64 {
		if k == "lookup" {
			return 0.1
		}
		return 0.9
	}}
	r, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q",
		Items: []db.Record{hyp(t, "h1", Control{None: "no baseline exists"}, "p1")}, Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	c := checkOf(r.Items[0], "control")
	if c == nil || c.By != "server" || c.Status != Unclear || c.Detail != "no control; at most caveated in Validation" {
		t.Fatalf("control: %+v", c)
	}
	if r.Items[0].Status != Unclear || Decide(r, 0) != DecideCarry {
		t.Errorf("item %s, decision %s", r.Items[0].Status, Decide(r, 0))
	}
}

func TestHypothesesMissingCiteFailsWithoutJev(t *testing.T) {
	a := &fakeAsker{}
	r, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q",
		Items: []db.Record{hyp(t, "h1", Control{Text: "v"}, "gone")}})
	if err != nil {
		t.Fatal(err)
	}
	if c := checkOf(r.Items[0], "cites_recorded"); c == nil || c.Status != Fail || r.Items[0].Status != Fail {
		t.Fatalf("item: %+v", r.Items[0])
	}
	if len(a.noul) != 0 {
		t.Error("Jev asked about an item failing the server check")
	}
}

func TestHypothesesWeakest(t *testing.T) {
	a := &fakeAsker{choice: "hyp:2"}
	r, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q",
		Items: []db.Record{hyp(t, "h1", Control{Text: "v"}, "p1"), hyp(t, "h2", Control{Text: "v"}, "p1")},
		Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Weakest == nil || r.Weakest.Key != "h2" || r.Weakest.P != 0.6 {
		t.Fatalf("weakest: %+v", r.Weakest)
	}
	c := a.choices[0]
	if c.Instructions != "Which of these hypotheses is least supported by its cited evidence?" || c.Options["hyp:1"] != "Hypothesis 1" {
		t.Errorf("choice: %+v", c)
	}
	for _, h := range []string{"claim h1", "claim h2"} {
		for k, v := range c.Options {
			if strings.Contains(k, h) || strings.Contains(v, h) {
				t.Errorf("option %s=%s carries hypothesis text", k, v)
			}
		}
		if !fenced(c.State, h) {
			t.Errorf("choice state lacks fenced %q: %s", h, c.State)
		}
	}
	a.choice = "hyp:9"
	if _, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q",
		Items: []db.Record{hyp(t, "h1", Control{Text: "v"}, "p1"), hyp(t, "h2", Control{Text: "v"}, "p1")},
		Cited: citedPaper(t, "p1")}); err == nil {
		t.Error("choice outside the options must error")
	}
}

// fenced reports whether an untrusted fence in state opens with text.
func fenced(state, text string) bool {
	return strings.Contains(state, "-BEGIN\n"+text)
}

func TestHypothesesNoItemsIsAFail(t *testing.T) {
	r, err := Hypotheses(context.Background(), &fakeAsker{}, HypothesesInput{Question: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if Decide(r, 0) != DecideRedo {
		t.Error("want redo")
	}
}
