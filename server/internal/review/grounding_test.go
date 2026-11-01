package review

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"lykeion/server/internal/sources"
	db "lykeion/server/pkg/db/generated"
)

func rec(t *testing.T, key, kind string, body any) db.Record {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return db.Record{Key: key, Kind: kind, Body: b, BoardColumn: "grounding", Role: "auditor"}
}

func paper(t *testing.T, key, id string) db.Record {
	return rec(t, key, KindPaper, PaperBody{ID: id, Text: "contributes " + id, Cites: []string{"c1"}})
}

func checkOf(it Item, id string) *Check {
	for i := range it.Checks {
		if it.Checks[i].ID == id {
			return &it.Checks[i]
		}
	}
	return nil
}

var crit3 = []string{"in adult mice", "measures liver weight", "published after 2010"}

func twoPapers() *fakeSources {
	return &fakeSources{papers: map[string]sources.Hit{
		"doi:10.1/a": {ID: "openalex:W1", DOI: "10.1/a", Title: "Paper A", Abstract: "abstract A"},
		"doi:10.1/b": {ID: "openalex:W2", DOI: "10.1/b", Title: "Paper B", Abstract: "abstract B"},
	}}
}

func TestGroundingServerFailSkipsJev(t *testing.T) {
	a := &fakeAsker{}
	s := &fakeSources{papers: map[string]sources.Hit{"doi:10.1/r": {ID: "openalex:W9", DOI: "10.1/r", Retracted: true}}}
	r, err := Grounding(context.Background(), a, s, GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{paper(t, "p1", "doi:10.1/gone"), paper(t, "p2", "doi:10.1/r")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 2 {
		t.Fatalf("items: %+v", r.Items)
	}
	if c := checkOf(r.Items[0], "exists"); c == nil || c.Status != Fail || c.By != "server" || r.Items[0].Status != Fail {
		t.Errorf("missing paper: %+v", r.Items[0])
	}
	if c := checkOf(r.Items[1], "not_retracted"); c == nil || c.Status != Fail || r.Items[1].Status != Fail {
		t.Errorf("retracted paper: %+v", r.Items[1])
	}
	for _, c := range a.noul {
		if strings.Contains(c.State, "doi:10.1/gone") || strings.Contains(c.State, "doi:10.1/r") {
			t.Errorf("Jev was asked about a paper that failed the server checks")
		}
	}
	if Decide(r, 0) != DecideRedo {
		t.Error("want redo")
	}
}

func TestGroundingAsksEachCriterion(t *testing.T) {
	a := &fakeAsker{choice: "source:2"}
	r, err := Grounding(context.Background(), a, twoPapers(), GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{paper(t, "p1", "doi:10.1/a"), paper(t, "p2", "doi:10.1/b")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.noul) != 2 {
		t.Fatalf("want 2 Noul calls, got %d", len(a.noul))
	}
	for _, c := range a.noul {
		keys := slices.Sorted(func(yield func(string) bool) {
			for k := range c.Questions {
				if !yield(k) {
					return
				}
			}
		})
		if !slices.Equal(keys, []string{"criterion:1", "criterion:2", "criterion:3"}) {
			t.Errorf("keys %v", keys)
		}
		for i, cr := range crit3 {
			want := "Does this source meet this condition of the research question: " + cr + "?"
			if got := c.Questions["criterion:"+string(rune('1'+i))]; got != want {
				t.Errorf("question %d = %q", i+1, got)
			}
		}
	}
	if !strings.Contains(a.noul[0].State, "Paper A") || !strings.Contains(a.noul[0].State, "abstract A") || !strings.HasPrefix(a.noul[0].State, "Stage: Grounding\n") {
		t.Errorf("state: %s", a.noul[0].State)
	}
	if len(a.choices) != 1 || a.choices[0].Options["source:1"] != "Source 1" || a.choices[0].Instructions != "Which of these sources is least supported as meeting the research question's conditions?" {
		t.Fatalf("choice: %+v", a.choices)
	}
	if r.Weakest == nil || *r.Weakest != (Weakest{Key: "p2", P: 0.6}) {
		t.Errorf("weakest %+v", r.Weakest)
	}
	if c := checkOf(r.Items[0], "criterion:2"); c == nil || c.By != "jev" || c.P == nil || *c.P != 0.9 || c.Status != Pass || c.Question == "" {
		t.Errorf("criterion check %+v", c)
	}
	if r.Column != "grounding" || Decide(r, 0) != DecidePass {
		t.Errorf("result %+v", r)
	}
}

func TestGroundingMissedPaper(t *testing.T) {
	s := twoPapers()
	s.hits = []sources.Hit{
		{ID: "openalex:W1", DOI: "10.1/A", Title: "Paper A"}, // cited: DOIs compare without case
		{ID: "openalex:W7", DOI: "10.1/missed", Title: "Missed one"},
		{ID: "openalex:W8", Title: "Fails a criterion"},
	}
	a := &fakeAsker{p: func(state, key string) float64 {
		if strings.Contains(state, "Fails a criterion") && key == "criterion:2" {
			return 0.1
		}
		return 0.9
	}}
	r, err := Grounding(context.Background(), a, s, GroundingInput{Question: "Does X?", Criteria: crit3,
		Items: []db.Record{paper(t, "p1", "doi:10.1/a")}})
	if err != nil {
		t.Fatal(err)
	}
	if s.queries[0] != "Does X? "+strings.Join(crit3, " ") {
		t.Errorf("query %q", s.queries[0])
	}
	if len(a.noul) != 3 {
		t.Errorf("want 1 item + 2 uncited candidates asked, got %d", len(a.noul))
	}
	if len(r.Missed) != 1 || r.Missed[0].Label != "10.1/missed" || r.Missed[0].Status != Fail || r.Missed[0].Key != "" {
		t.Fatalf("missed %+v", r.Missed)
	}
	if Decide(r, 0) != DecideRedo {
		t.Error("want redo")
	}
	if !strings.Contains(RedoNote(r), "10.1/missed") {
		t.Errorf("redo note: %s", RedoNote(r))
	}
}

func TestGroundingScreensAtMost20Candidates(t *testing.T) {
	s := &fakeSources{}
	for i := range 30 {
		s.hits = append(s.hits, sources.Hit{ID: "openalex:W" + string(rune('a'+i))})
	}
	a := &fakeAsker{}
	if _, err := Grounding(context.Background(), a, s, GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{rec(t, "n", KindNoQualifying, NoQualifyingBody{Text: "none"})}}); err != nil {
		t.Fatal(err)
	}
	if len(a.noul) != 20 {
		t.Errorf("screened %d", len(a.noul))
	}
}

func TestNoQualifyingWithACandidateFails(t *testing.T) {
	nq := rec(t, "n", KindNoQualifying, NoQualifyingBody{Text: "No paper studies adult mice."})
	s := &fakeSources{hits: []sources.Hit{{ID: "openalex:W7", DOI: "10.1/missed"}}}
	r, err := Grounding(context.Background(), &fakeAsker{}, s, GroundingInput{Question: "q", Criteria: crit3, Items: []db.Record{nq}})
	if err != nil {
		t.Fatal(err)
	}
	if c := checkOf(r.Items[0], "none_qualify"); c == nil || c.Status != Fail || r.Items[0].Status != Fail || r.Items[0].Label != "No paper studies adult mice." {
		t.Errorf("item %+v", r.Items[0])
	}

	r, err = Grounding(context.Background(), &fakeAsker{p: func(string, string) float64 { return 0.1 }}, s,
		GroundingInput{Question: "q", Criteria: crit3, Items: []db.Record{nq}})
	if err != nil {
		t.Fatal(err)
	}
	if c := checkOf(r.Items[0], "none_qualify"); c == nil || c.Status != Pass || len(r.Missed) != 0 || Decide(r, 0) != DecidePass {
		t.Errorf("with no candidate passing: %+v", r)
	}
}

func TestNoItemsIsAFail(t *testing.T) {
	s := &fakeSources{}
	r, err := Grounding(context.Background(), &fakeAsker{}, s, GroundingInput{Question: "q", Criteria: crit3})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 0 || Decide(r, 0) != DecideRedo {
		t.Errorf("result %+v", r)
	}

	carried := Item{Key: "p0", Kind: KindPaper, Label: "doi:10.1/a", Status: Pass}
	r, err = Grounding(context.Background(), &fakeAsker{}, s, GroundingInput{Question: "q", Criteria: crit3, Carried: []Item{carried}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || !r.Items[0].Carried || Decide(r, 0) != DecidePass {
		t.Errorf("carried only: %+v", r)
	}
}

func TestNoCriteriaUsesTheQuestion(t *testing.T) {
	a := &fakeAsker{}
	s := twoPapers()
	_, err := Grounding(context.Background(), a, s, GroundingInput{Question: "Does fasting shrink the liver?",
		Items: []db.Record{paper(t, "p1", "doi:10.1/a")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.noul) != 1 || len(a.noul[0].Questions) != 1 {
		t.Fatalf("noul %+v", a.noul)
	}
	q := a.noul[0].Questions["criterion:1"]
	if !strings.HasPrefix(q, "Does this source bear directly on the research question as it was asked") || !strings.Contains(q, "Does fasting shrink the liver") {
		t.Errorf("question %q", q)
	}
	if s.queries[0] != "Does fasting shrink the liver?" {
		t.Errorf("query %q", s.queries[0])
	}
}

func TestGroundingStateIsBlind(t *testing.T) {
	a := &fakeAsker{choice: "source:1"}
	in := GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{paper(t, "p1", "doi:10.1/a"), paper(t, "p2", "doi:10.1/b"),
			rec(t, "d1", KindDataset, DatasetBody{Accession: "GSE1", Text: "liver data"})}}
	for i := range in.Items {
		in.Items[i].Role = "auditor Team Hepatica claude-opus"
	}
	s := twoPapers()
	s.datasets = map[string]sources.Hit{"GSE1": {ID: "GSE1", Abstract: "accession: GSE1"}}
	if _, err := Grounding(context.Background(), a, s, in); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, c := range a.noul {
		seen = append(seen, c.State)
		for _, q := range c.Questions {
			seen = append(seen, q)
		}
	}
	for _, c := range a.choices {
		seen = append(seen, c.State, c.Instructions)
		for _, o := range c.Options {
			seen = append(seen, o)
		}
	}
	if len(a.noul) != 3 || len(a.choices) != 1 {
		t.Fatalf("asked %d, chose %d", len(a.noul), len(a.choices))
	}
	for _, s := range seen {
		for _, bad := range []string{"auditor", "Hepatica", "claude", "opus"} {
			if strings.Contains(strings.ToLower(s), strings.ToLower(bad)) {
				t.Errorf("Jev saw %q in %q", bad, s)
			}
		}
	}
}

func TestGroundingChoiceOptionsAreNeutral(t *testing.T) {
	a := &fakeAsker{choice: "source:1"}
	r, err := Grounding(context.Background(), a, twoPapers(), GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{paper(t, "p1", "doi:10.1/a"), paper(t, "p2", "doi:10.1/b")}})
	if err != nil {
		t.Fatal(err)
	}
	c := a.choices[0]
	if len(c.Options) != 2 || c.Options["source:2"] != "Source 2" {
		t.Fatalf("options %v", c.Options)
	}
	for k, o := range c.Options {
		for _, agent := range []string{"doi:10.1/a", "doi:10.1/b", "p1", "p2", "contributes"} {
			if strings.Contains(k+o, agent) {
				t.Errorf("option %s=%q holds the agent's %q", k, o, agent)
			}
		}
	}
	if !strings.Contains(c.State, "doi:10.1/a") || !strings.Contains(c.State, "Source 2") || strings.Count(c.State, "-BEGIN\n") < 3 {
		t.Errorf("each source is fenced in the state: %s", c.State)
	}
	if r.Weakest == nil || r.Weakest.Key != "p1" {
		t.Errorf("weakest %+v", r.Weakest)
	}
}

func TestGroundingRejectsAnUnknownChoice(t *testing.T) {
	_, err := Grounding(context.Background(), &fakeAsker{choice: "p1"}, twoPapers(), GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{paper(t, "p1", "doi:10.1/a"), paper(t, "p2", "doi:10.1/b")}})
	if err == nil {
		t.Error("a choice outside the options: no error")
	}
}

func TestGroundingOutageIsAnError(t *testing.T) {
	ds := rec(t, "d1", KindDataset, DatasetBody{Accession: "GSE1", Text: "liver data"})
	for name, in := range map[string]GroundingInput{
		"paper":   {Items: []db.Record{paper(t, "p1", "doi:10.1/down")}},
		"dataset": {Items: []db.Record{ds}},
		"carried": {Carried: []Item{{Key: "p0", Kind: KindPaper, Label: "doi:10.1/down", Status: Pass}}},
	} {
		s := &fakeSources{outage: map[string]bool{"doi:10.1/down": true, "GSE1": true}}
		in.Question, in.Criteria = "q", crit3
		if _, err := Grounding(context.Background(), &fakeAsker{}, s, in); err == nil {
			t.Errorf("%s: an upstream outage gave no error", name)
		}
	}
	r, err := Grounding(context.Background(), &fakeAsker{}, &fakeSources{}, GroundingInput{Question: "q", Criteria: crit3, Items: []db.Record{ds}})
	if err != nil {
		t.Fatal(err)
	}
	if c := checkOf(r.Items[0], "exists"); c == nil || c.Status != Fail {
		t.Errorf("an unknown accession: %+v", r.Items[0])
	}
}

func TestGroundingCarriedPmidIsNotMissed(t *testing.T) {
	s := &fakeSources{
		papers: map[string]sources.Hit{"pmid:123": {ID: "europepmc:MED:123", DOI: "10.5/Liver", PMID: "123"}},
		hits:   []sources.Hit{{ID: "openalex:W5", DOI: "10.5/liver"}},
	}
	carried := Item{Key: "p0", Kind: KindPaper, Label: "pmid:123", Status: Pass}
	r, err := Grounding(context.Background(), &fakeAsker{}, s, GroundingInput{Question: "q", Criteria: crit3, Carried: []Item{carried}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Missed) != 0 || Decide(r, 0) != DecidePass {
		t.Errorf("the carried paper came back as missed: %+v", r.Missed)
	}
}

func TestGroundingPmidWithoutDOIIsNotMissed(t *testing.T) {
	s := &fakeSources{
		papers: map[string]sources.Hit{"pmid:456": {ID: "europepmc:MED:456", Title: "No DOI", PMID: "456"}},
		hits:   []sources.Hit{{ID: "openalex:W6", PMID: "456"}},
	}
	r, err := Grounding(context.Background(), &fakeAsker{}, s, GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{paper(t, "p1", "pmid:456")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Missed) != 0 {
		t.Errorf("the declared paper came back as missed: %+v", r.Missed)
	}
}

func TestEveryNoQualifyingIsChecked(t *testing.T) {
	s := &fakeSources{hits: []sources.Hit{{ID: "openalex:W7"}}}
	r, err := Grounding(context.Background(), &fakeAsker{}, s, GroundingInput{Question: "q", Criteria: crit3,
		Items: []db.Record{rec(t, "n1", KindNoQualifying, NoQualifyingBody{Text: "none"}), rec(t, "n2", KindNoQualifying, NoQualifyingBody{Text: "still none"})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range r.Items {
		if c := checkOf(it, "none_qualify"); c == nil || it.Status != Fail {
			t.Errorf("%s: %+v", it.Key, it)
		}
	}
}
