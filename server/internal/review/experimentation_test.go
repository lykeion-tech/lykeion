package review

import (
	"context"
	"testing"
	"time"

	db "lykeion/server/pkg/db/generated"
)

func outputs(t *testing.T, key string, at time.Time, files ...string) db.Record {
	r := rec(t, key, KindOutputs, OutputsBody{Files: files})
	r.CreatedAt = at
	return r
}

func TestMissingDeclaredFileFails(t *testing.T) {
	now := time.Now()
	r, err := Experimentation(context.Background(), &fakeAsker{}, ExperimentationInput{
		Declared: []db.Record{outputs(t, "d2", now, "fig.png", "metrics.csv"), outputs(t, "d1", now.Add(-time.Hour), "old.txt")},
		Saved:    []string{"metrics.csv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	it := r.Items[0]
	if it.Kind != KindOutputs || it.Key != "d2" || it.Status != Fail {
		t.Fatalf("outputs item: %+v", it)
	}
	if c := checkOf(it, "file:fig.png"); c == nil || c.Status != Fail || c.By != "server" {
		t.Errorf("fig.png: %+v", c)
	}
	if checkOf(it, "file:metrics.csv") != nil || checkOf(it, "file:old.txt") != nil {
		t.Errorf("only missing files of the latest record fail: %+v", it.Checks)
	}
	if Decide(r, 0) != DecideRedo {
		t.Error("want redo")
	}
	r, err = Experimentation(context.Background(), &fakeAsker{}, ExperimentationInput{
		Declared: []db.Record{outputs(t, "d2", now, "fig.png")}, Saved: []string{"fig.png"}})
	if err != nil || r.Items[0].Status != Pass || Decide(r, 0) != DecidePass {
		t.Fatalf("all saved: %+v, %v", r, err)
	}
}

func TestValuesOnlyHandInPassesDeclaredOutputs(t *testing.T) {
	r, err := Experimentation(context.Background(), &fakeAsker{}, ExperimentationInput{
		Values: []ValueCheck{{Record: "r1", Value: "0.42", Sentence: "AUC was 0.42", Found: true, Where: "run:x1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || r.Items[0].Kind != "value" || Decide(r, 0) != DecidePass {
		t.Fatalf("values only: %+v", r)
	}
	r, err = Experimentation(context.Background(), &fakeAsker{}, ExperimentationInput{Saved: []string{"a.csv"}})
	if err != nil {
		t.Fatal(err)
	}
	if Decide(r, 0) != DecideRedo || len(Reasons(r)) != 1 || Reasons(r)[0] != "The Team handed in no outputs or values." {
		t.Fatalf("neither: %+v %q", r, Reasons(r))
	}
}

func TestUntracedValueFails(t *testing.T) {
	a := &fakeAsker{}
	r, err := Experimentation(context.Background(), a, ExperimentationInput{
		Declared: []db.Record{outputs(t, "d1", time.Now(), "a.csv")}, Saved: []string{"a.csv"},
		Values: []ValueCheck{{Record: "r1", Value: "0.42", Sentence: "AUC was 0.42"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	it := r.Items[1]
	if it.Key != "r1" || it.Status != Fail {
		t.Fatalf("value item: %+v", it)
	}
	if c := checkOf(it, "traced"); c == nil || c.Status != Fail || c.By != "server" {
		t.Errorf("traced: %+v", c)
	}
	if len(a.noul) != 0 || checkOf(it, "described") != nil {
		t.Errorf("an untraced value is not sent to Jev: %d calls", len(a.noul))
	}
}

func TestCategoricalValueAsksAboutOutcomeNotNumber(t *testing.T) {
	a := &fakeAsker{}
	r, err := Experimentation(context.Background(), a, ExperimentationInput{
		Values: []ValueCheck{{Record: "r1", Value: "A", Sentence: "Outcome", Found: true, Where: "run:x1", Textual: true}},
	})
	if err != nil || len(r.Items) != 1 {
		t.Fatalf("review: %+v %v", r, err)
	}
	q := a.noul[0].Questions["described"]
	if q != "Does the sentence describe this outcome correctly?" {
		t.Fatalf("question: %s", q)
	}
}

func TestMisdescribedNumberOnlyWarns(t *testing.T) {
	a := &fakeAsker{p: func(_, _ string) float64 { return 0.1 }}
	r, err := Experimentation(context.Background(), a, ExperimentationInput{
		Declared: []db.Record{outputs(t, "d1", time.Now(), "a.csv")}, Saved: []string{"a.csv"},
		Values: []ValueCheck{{Record: "r1", Value: "0.42", Found: true, Where: "run:x3",
			Sentence: "Error fell to 0.42 ms", Context: "accuracy 0.42"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	it := r.Items[1]
	if c := checkOf(it, "traced"); c == nil || c.Status != Pass {
		t.Errorf("traced: %+v", c)
	}
	c := checkOf(it, "described")
	if c == nil || !c.Warning || c.By != "jev" || c.Status != Fail || c.P == nil || *c.P != 0.1 {
		t.Fatalf("described: %+v", c)
	}
	if it.Status != Pass || Decide(r, 0) != DecidePass {
		t.Errorf("a warning failed the review: item %s, decision %s", it.Status, Decide(r, 0))
	}
	if len(a.noul) != 1 {
		t.Fatalf("want 1 Noul call, got %d", len(a.noul))
	}
	q := a.noul[0].Questions
	if len(q) != 1 || q["described"] != "Does the sentence describe this number correctly: what it measures, its direction and its unit?" {
		t.Errorf("questions: %v", q)
	}
	if s := a.noul[0].State; !fenced(s, "Value: 0.42\nError fell to 0.42 ms") || !fenced(s, "accuracy 0.42") {
		t.Errorf("sentence and context not fenced: %s", s)
	}
}

type silentAsker struct{ *fakeAsker }

func (silentAsker) Noul(context.Context, string, map[string]string) (map[string]float64, error) {
	return map[string]float64{}, nil
}

func TestDescribedWithoutAnAnswerIsAnError(t *testing.T) {
	_, err := Experimentation(context.Background(), silentAsker{&fakeAsker{}}, ExperimentationInput{
		Values: []ValueCheck{{Record: "r1", Value: "1", Found: true, Sentence: "n was 1"}}})
	if err == nil {
		t.Fatal("want an error")
	}
}
