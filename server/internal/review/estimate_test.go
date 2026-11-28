package review

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"strings"
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	db "lykeion/server/pkg/db/generated"
)

const estMeasure = `{"id":"prob_pr_65","name":"predicted probability of PR at 65","unit":"fraction"}`

func estBody(extra string) string {
	return `{"text":"t","prediction":{"shape":"estimate"},"control":{},"cites":["a"],"measure":` + estMeasure +
		`,"method":"logistic regression of PR on age, all patients"` + extra + `}`
}

func TestEstimateHypothesisRecord(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"valid", estBody(""), ""},
		{"valid with reported", estBody(`,"reported":{"value":0.39,"source":"doi:10.1/a","key":"r3"}`), ""},
		{"no measure", strings.Replace(estBody(""), `,"measure":`+estMeasure, "", 1), "an estimate needs a measure"},
		{"no method", strings.Replace(estBody(""), `"logistic regression of PR on age, all patients"`, `""`, 1), "an estimate needs a method"},
		{"long method", strings.Replace(estBody(""), `"logistic regression of PR on age, all patients"`, `"`+strings.Repeat("x", 2001)+`"`, 1), "method"},
		{"with low", strings.Replace(estBody(""), `{"shape":"estimate"}`, `{"shape":"estimate","low":1}`, 1), "an estimate predicts no value"},
		{"with direction", strings.Replace(estBody(""), `{"shape":"estimate"}`, `{"shape":"estimate","direction":"increase"}`, 1), "an estimate predicts no value"},
		{"with comparison", estBody(`,"comparison":{"threshold":1,"less":"a","equal":"b","greater":"c"}`), "an estimate predicts no value"},
		{"method on direction", `{"text":"t","prediction":{"shape":"direction","direction":"increase"},"control":{"text":"c"},"cites":["a"],"method":"m"}`, "only an estimate has a method"},
		{"direction still needs control", `{"text":"t","prediction":{"shape":"direction","direction":"increase"},"control":{},"cites":["a"]}`, "control needs exactly one of text or none"},
	}
	for _, c := range cases {
		err := Validate(KindHypothesis, board.Hypotheses, []byte(c.body))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

// Stored bodies of the three earlier shapes must serialize exactly as before.
func TestOldHypothesisBodiesSerializeUnchanged(t *testing.T) {
	lo, hi := 0.5, 0.8
	for _, c := range []struct {
		b    HypothesisBody
		want string
	}{
		{HypothesisBody{Text: "h", Prediction: Prediction{Shape: "range", Low: &lo, High: &hi, Uncertainty: "90%"}, Control: Control{Text: "c"}, Cites: []string{"r1"}, Measure: &Measure{ID: "m", Name: "M", Unit: "u"}},
			`{"text":"h","prediction":{"shape":"range","low":0.5,"high":0.8,"uncertainty":"90%"},"control":{"text":"c"},"cites":["r1"],"measure":{"id":"m","name":"M","unit":"u"}}`},
		{HypothesisBody{Text: "h", Prediction: Prediction{Shape: "direction", Direction: "increase"}, Control: Control{None: "none"}, Cites: []string{"r1"}},
			`{"text":"h","prediction":{"shape":"direction","direction":"increase"},"control":{"none":"none"},"cites":["r1"]}`},
	} {
		got, _ := json.Marshal(c.b)
		if string(got) != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
	if !IsEstimate(HypothesisBody{Prediction: Prediction{Shape: "estimate"}}) || IsEstimate(HypothesisBody{Prediction: Prediction{Shape: "range"}}) {
		t.Error("IsEstimate")
	}
}

func estHyp(t *testing.T, key string, cites ...string) db.Record {
	return rec(t, key, KindHypothesis, HypothesisBody{Text: "estimate " + key,
		Prediction: Prediction{Shape: "estimate"}, Cites: cites,
		Measure:  &Measure{ID: "prob_pr_65", Name: "PR probability at 65", Unit: "fraction"},
		Method:   "logistic regression of PR on age, all patients",
		Reported: &ReportedValue{Value: 0.39, Source: "doi:10.1/a", Key: "p1"}})
}

func TestEstimateQuestions(t *testing.T) {
	a := &fakeAsker{}
	r, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q", Items: []db.Record{estHyp(t, "e1", "p1")}, Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"fits":      "Does the research question ask for this quantity to be measured, rather than for a claim to be tested?",
		"justified": "Does the cited evidence justify measuring this quantity this way?",
		"specified": "Is the method specified precisely enough that another analyst would compute the same number?",
		"reported":  "Does the cited source report this value for this measure?",
	}
	if len(a.noul) != 1 || !maps.Equal(a.noul[0].Questions, want) {
		t.Fatalf("questions: %+v", a.noul)
	}
	s := a.noul[0].State
	for _, w := range []string{"Estimate: PR probability at 65 (fraction) by logistic regression of PR on age, all patients", "Published value: 0.39 (doi:10.1/a)"} {
		if !strings.Contains(s, w) {
			t.Errorf("state lacks %q: %s", w, s)
		}
	}
	for _, c := range r.Items[0].Checks {
		if c.ID == "lookup" || c.Inverted {
			t.Errorf("estimate asked lookup: %+v", c)
		}
	}
}

func TestEstimateReviewShrinksOversizedEvidence(t *testing.T) {
	in := HypothesesInput{Question: "What is the rate?", Items: []db.Record{estHyp(t, "e1", "p1")},
		Cited: map[string]db.Record{"p1": rec(t, "p1", "call", map[string]string{"answer": strings.Repeat("source data 0123456789 ", 5000)})}}
	judge := &fakeAsker{maxState: MaxState / 8}
	r, err := Hypotheses(t.Context(), judge, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(judge.noul) < 2 || r.Items[0].Status != Pass {
		t.Fatalf("got %d requests and review %+v", len(judge.noul), r.Items)
	}
	first, last := judge.noul[0], judge.noul[len(judge.noul)-1]
	if len(last.State) >= len(first.State) || !maps.Equal(first.Questions, last.Questions) {
		t.Fatal("retry must shrink the evidence and keep the judgment questions")
	}
	for _, want := range []string{"What is the rate?", "logistic regression of PR on age", "source data", "[cut]", "Only the visible excerpts are available for this judgment."} {
		if !strings.Contains(last.State, want) {
			t.Errorf("smaller state lost %q", want)
		}
	}
	judge = &fakeAsker{maxState: 1}
	if _, err := Hypotheses(t.Context(), judge, in); !errors.Is(err, jev.ErrTooLong) || len(judge.noul) > 5 {
		t.Fatalf("permanently oversized state: %v after %d attempts", err, len(judge.noul))
	}
}

func TestMixedEstimateAndPredictionWeakest(t *testing.T) {
	a := &fakeAsker{choice: "hyp:2"}
	r, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q",
		Items: []db.Record{hyp(t, "h1", Control{Text: "v"}, "p1"), estHyp(t, "e2", "p1")}, Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.noul) != 2 || a.noul[0].Questions["lookup"] == "" || a.noul[1].Questions["fits"] == "" || a.noul[1].Questions["lookup"] != "" {
		t.Fatalf("question sets: %+v", a.noul)
	}
	if len(a.choices) != 1 || r.Weakest == nil || r.Weakest.Key != "e2" {
		t.Fatalf("weakest over both: %+v %+v", a.choices, r.Weakest)
	}
}

func TestEstimateLowFitsFails(t *testing.T) {
	a := &fakeAsker{p: func(_, k string) float64 {
		if k == "fits" {
			return 0.1
		}
		return 0.9
	}}
	r, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q", Items: []db.Record{estHyp(t, "e1", "p1")}, Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Items[0].Status != Fail {
		t.Fatalf("a test question handed in as an estimate passed: %+v", r.Items[0])
	}
}

func estimateBody() HypothesisBody {
	return HypothesisBody{Text: "e", Prediction: Prediction{Shape: "estimate"},
		Measure: &Measure{ID: "prob_pr_65", Name: "PR probability at 65", Unit: "fraction"}, Method: "logistic regression on age"}
}

func boundGood(v float64, measure string) Measured {
	m := good(v)
	m.Bound, m.BoundValue, m.MeasureID = true, strconv.FormatFloat(v, 'f', -1, 64), measure
	return m
}

func TestStatusesEstimate(t *testing.T) {
	h := estimateBody()
	r := run(t, h, &Assessment{Hypothesis: "e", Measurement: "m"}, map[string]Measured{"m": boundGood(0.395, "prob_pr_65")})
	if r.Status != Supported || r.Reasons[0] != "measured 0.395 fraction by logistic regression on age; reproduced on replay" {
		t.Fatalf("%+v", r)
	}
}

func TestStatusesEstimateUnboundOrNotReproduced(t *testing.T) {
	h := estimateBody()
	other := boundGood(0.395, "recall")
	stale := boundGood(0.395, "prob_pr_65")
	stale.Reproduced, stale.RerunNote = false, "differs"
	gone := boundGood(0.395, "prob_pr_65")
	gone.Reproduced, gone.ReplayUnavailable, gone.RerunNote = false, true, "artifact not kept"
	for name, tc := range map[string]struct {
		v      Measured
		want   string
		reason string
	}{
		"unbound":         {good(0.395), Invalid, "not bound to the estimate's measure"},
		"other measure":   {other, Invalid, "different hypothesis measure"},
		"not reproduced":  {stale, Invalid, "not reproduced"},
		"replay unavaila": {gone, Inconclusive, "could not be checked"},
	} {
		r := run(t, h, &Assessment{Hypothesis: "e", Measurement: "m"}, map[string]Measured{"m": tc.v})
		if r.Status != tc.want || !strings.Contains(strings.Join(r.Reasons, "; "), tc.reason) {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestStatusesEstimateNeverCapped(t *testing.T) {
	h := estimateBody()
	h.Control = Control{None: "not applicable"}
	r := run(t, h, &Assessment{Hypothesis: "e", Measurement: "m"}, map[string]Measured{"m": boundGood(0.395, "prob_pr_65")})
	if r.Status != Supported || strings.Contains(strings.Join(r.Reasons, " "), "no control") {
		t.Fatalf("%+v", r)
	}
}

func TestStatusesEstimatePublishedValue(t *testing.T) {
	for published, want := range map[float64]string{
		0.39:  "published value 0.39 (doi:10.1/a) differs by 1.3%",
		0.395: "published value 0.395 (doi:10.1/a) matches",
	} {
		h := estimateBody()
		h.Reported = &ReportedValue{Value: published, Source: "doi:10.1/a", Key: "p1"}
		r := run(t, h, &Assessment{Hypothesis: "e", Measurement: "m"}, map[string]Measured{"m": boundGood(0.395, "prob_pr_65")})
		if r.Status != Supported || len(r.Reasons) < 2 || r.Reasons[1] != want {
			t.Errorf("%v: %+v", published, r)
		}
	}
}

// Final review: an estimate needs no control, so naming none is no caveat;
// and a value attributed to a source is put to Jev against that source.
func TestEstimateNoControlIsNoCaveatAndReportedIsChecked(t *testing.T) {
	r := estHyp(t, "e1", "p1")
	b := records.Body[HypothesisBody](r)
	b.Control = Control{None: "not applicable"}
	b.Reported = nil
	r = rec(t, "e1", KindHypothesis, b)
	a := &fakeAsker{}
	res, err := Hypotheses(context.Background(), a, HypothesesInput{Question: "q", Items: []db.Record{r}, Cited: citedPaper(t, "p1")})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Items[0].Checks {
		if c.ID == "control" {
			t.Errorf("estimate got a no-control check: %+v", c)
		}
	}
	if _, asked := a.noul[0].Questions["reported"]; asked {
		t.Error("asked about a reported value the estimate does not have")
	}
}
