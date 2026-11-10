package report

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
)

// fakeJev answers Noul with p(state) and Choice with choice at choiceP, and
// keeps every state it was asked about.
type fakeJev struct {
	p       func(state string) float64
	choice  string
	choiceP float64
	states  []string
	asks    int
}

func (f *fakeJev) Noul(_ context.Context, state string, qs map[string]string) (map[string]float64, error) {
	f.asks++
	f.states = append(f.states, state)
	out := map[string]float64{}
	for k := range qs {
		out[k] = f.p(state)
	}
	return out, nil
}

func (f *fakeJev) Choice(_ context.Context, state, _ string, _ map[string]string) (jev.Answer, error) {
	f.asks++
	f.states = append(f.states, state)
	return jev.Answer{Choice: f.choice, Probabilities: map[string]float64{f.choice: f.choiceP}}, nil
}

func judgeFixture() (Report, Proposals) {
	rep := Report{
		HandIn: &HandIn{Text: "We found that IL6 rises. It was clear."},
		Nodes: []Node{
			{ID: "rec:a1", Kind: "paper", Label: "arxiv:1", Quote: "IL6 increases inflammation in mice."},
			{ID: "rec:a2", Kind: "paper", Label: "arxiv:2", Quote: "TNF decreases weight."},
		},
	}
	p := Proposals{
		Concepts: []handin.ReportConcept{{Key: "il6", Label: "IL6", Type: "protein"}, {Key: "infl", Label: "Inflammation", Type: "disease"},
			{Key: "tnf", Label: "TNF", Type: "protein"}, {Key: "weight", Label: "Weight", Type: "measure"}, {Key: "il6", Label: "IL6 again", Type: "protein"}},
		Facts: []handin.ReportFact{
			{Subject: "il6", Relation: "increases", Object: "infl", Quote: "IL6 increases inflammation", SourceNode: "rec:a1"},
			{Subject: "tnf", Relation: "decreases", Object: "weight", Quote: "TNF decreases weight", SourceNode: "rec:a2"},
		},
	}
	return rep, p
}

func TestJudgeKeepsOnlyAccepted(t *testing.T) {
	rep, p := judgeFixture()
	f := &fakeJev{p: func(s string) float64 {
		if strings.Contains(s, "il6 increases infl") {
			return 0.95
		}
		return 0.4
	}}
	j, err := Judge(context.Background(), f, rep, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Facts) != 1 || j.Facts[0].Subject != "il6" || j.Facts[0].P != 0.95 {
		t.Fatalf("facts %+v", j.Facts)
	}
	var keys []string
	for _, c := range j.Concepts {
		keys = append(keys, c.Key)
	}
	if strings.Join(keys, ",") != "il6,infl" {
		t.Fatalf("concepts %v", keys)
	}
}

func TestJudgeAsksAndKeepsDuplicatesOnce(t *testing.T) {
	rep, p := judgeFixture()
	p.Facts = append(p.Facts, p.Facts[0])
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	j, err := Judge(context.Background(), f, rep, p)
	if err != nil {
		t.Fatal(err)
	}
	if f.asks != 2 || len(j.Facts) != 2 || len(j.Concepts) != 4 {
		t.Fatalf("%d asks, %d facts, %d concepts kept; want 2, 2 and 4", f.asks, len(j.Facts), len(j.Concepts))
	}
}

func TestJudgeStatesAreFenced(t *testing.T) {
	rep, p := judgeFixture()
	p.Links = []Span{{Start: 14, End: 17, Node: "rec:a1", Phrase: "IL6"}}
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	j, err := Judge(context.Background(), f, rep, p)
	if err != nil {
		t.Fatal(err)
	}
	if f.asks != 3 || len(j.Links) != 1 || j.Links[0].P != 0.9 {
		t.Fatalf("%d asks, links %+v", f.asks, j.Links)
	}
	for _, s := range f.states {
		if !strings.Contains(s, "UNTRUSTED") {
			t.Fatalf("state not fenced:\n%s", s)
		}
	}
	quotes := []string{"IL6 increases inflammation", "TNF decreases weight", "We found that IL6 rises."}
	for i, q := range quotes {
		if !fencedContains(f.states[i], q) {
			t.Fatalf("state %d does not fence %q:\n%s", i, q, f.states[i])
		}
	}
}

// fencedContains reports whether q appears inside a fenced block of state.
func fencedContains(state, q string) bool {
	i := strings.Index(state, q)
	if i < 0 {
		return false
	}
	begin, end := strings.LastIndex(state[:i], "-BEGIN\n"), strings.LastIndex(state[:i], "-END\n")
	return begin > end && strings.Contains(state[i:], "-END\n")
}

func TestJudgeStopsAtMaxQuestions(t *testing.T) {
	rep, p := judgeFixture()
	p.Facts = nil
	for i := range 250 {
		p.Facts = append(p.Facts, handin.ReportFact{Subject: "il6", Relation: "increases", Object: "infl",
			Quote: fmt.Sprintf("IL6 increases inflammation %d", i), SourceNode: "rec:a1"})
	}
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	j, err := Judge(context.Background(), f, rep, p)
	if err != nil {
		t.Fatal(err)
	}
	if f.asks != MaxJevQuestions || len(j.Facts) != MaxJevQuestions {
		t.Fatalf("%d asks, %d facts; want %d", f.asks, len(j.Facts), MaxJevQuestions)
	}
}

func findingsFixture() Report {
	return Report{
		Nodes: []Node{
			{ID: "rec:p1", Kind: "paper", Label: "arxiv:1"},
			{ID: "rec:p2", Kind: "paper", Label: "arxiv:2"},
			{ID: "out:plot.png", Kind: "output", Label: "plot.png", ContentType: "image/png"},
			{ID: "out:data.csv", Kind: "output", Label: "data.csv", ContentType: "text/csv"},
			{ID: "rec:f1", Kind: "finding", Label: "IL6 rises", Quote: "IL6 rises"},
		},
		Edges: []Edge{{From: "out:plot.png", To: "rec:f1", Kind: "supports"}, {From: "out:data.csv", To: "rec:f1", Kind: "supports"}},
	}
}

func TestCheckFindingsFigureTableNovelty(t *testing.T) {
	texts := map[string]string{"out:plot.png": "A bar plot of IL6 by group.", "out:data.csv": "group,il6\na,1\nb,3\n",
		"paper:rec:p1": "IL6 falls with age.", "paper:rec:p2": "Unrelated work."}
	f := &fakeJev{p: func(string) float64 { return 0.2 }, choice: "contradicts", choiceP: 0.9}
	cs, err := CheckFindings(context.Background(), f, findingsFixture(), texts)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FindingCheck{}
	for _, c := range cs {
		if c.Node != "rec:f1" {
			t.Fatalf("check on %s", c.Node)
		}
		got[c.CheckID] = c
	}
	if len(cs) != 4 {
		t.Fatalf("checks %+v", cs)
	}
	fig, tab := got["figure:out:plot.png"], got["table:out:data.csv"]
	if fig.Question != "Does this figure, as its recorded text describes it, show what the claim says?" || fig.Status != "fail" || fig.P != 0.2 {
		t.Fatalf("figure %+v", fig)
	}
	if tab.Question != "Does this table's data match the claim?" || tab.Status != "fail" || tab.Evidence != texts["out:data.csv"] {
		t.Fatalf("table %+v", tab)
	}
	for _, id := range []string{"novelty:rec:p1", "novelty:rec:p2"} {
		if c := got[id]; c.Choice != "contradicts" || c.Status != "pass" || c.P != 0.9 {
			t.Fatalf("%s %+v", id, c)
		}
	}
}

func TestCheckFindingsStatuses(t *testing.T) {
	for _, tc := range []struct {
		p       float64
		fig, nv string
	}{{0.95, "pass", "pass"}, {0.5, "unclear", "unclear"}, {0.1, "fail", "unclear"}} {
		f := &fakeJev{p: func(string) float64 { return tc.p }, choice: "confirms", choiceP: tc.p}
		cs, err := CheckFindings(context.Background(), f, findingsFixture(),
			map[string]string{"out:plot.png": "a plot", "paper:rec:p1": "abstract"})
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 2 || cs[0].Status != tc.fig || cs[1].Status != tc.nv {
			t.Fatalf("p %v: %+v", tc.p, cs)
		}
	}
}

func TestCheckFindingsSkipsUntextedOutput(t *testing.T) {
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	cs, err := CheckFindings(context.Background(), f, findingsFixture(), map[string]string{"out:data.csv": "a,b\n1,2\n"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].CheckID != "table:out:data.csv" || f.asks != 1 {
		t.Fatalf("%d asks, checks %+v", f.asks, cs)
	}
}
