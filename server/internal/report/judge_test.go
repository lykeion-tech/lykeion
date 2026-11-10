package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/review"
)

// fakeJev answers Noul with p(state) and Choice with choice at choiceP, and
// keeps every state it was asked about.
type fakeJev struct {
	p       func(state string) float64
	imgErr  error // what NoulImages fails with, if anything
	choice  string
	choiceP float64
	states  []string
	asks    int
	images  []map[string]jev.Image // the images each ask showed, nil for none
	kinds   []string               // "noul", "image" or "choice", one per ask
}

func (f *fakeJev) NoulImages(ctx context.Context, state string, qs map[string]string, images map[string]jev.Image) (map[string]float64, error) {
	out, err := f.Noul(ctx, state, qs)
	if f.imgErr != nil {
		out, err = nil, f.imgErr
	}
	f.images[len(f.images)-1], f.kinds[len(f.kinds)-1] = images, "image"
	return out, err
}

func (f *fakeJev) Noul(_ context.Context, state string, qs map[string]string) (map[string]float64, error) {
	f.asks++
	f.states, f.images, f.kinds = append(f.states, state), append(f.images, nil), append(f.kinds, "noul")
	out := map[string]float64{}
	for k := range qs {
		out[k] = f.p(state)
	}
	return out, nil
}

func (f *fakeJev) Choice(_ context.Context, state, _ string, _ map[string]string) (jev.Answer, error) {
	f.asks++
	f.states, f.images, f.kinds = append(f.states, state), append(f.images, nil), append(f.kinds, "choice")
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
		if strings.Contains(s, "IL6 (protein) increases Inflammation (disease)") {
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

func TestQuestionBudgetsSumToTheCap(t *testing.T) {
	if JudgeQuestions+CheckQuestions != MaxJevQuestions || MaxJevQuestions != 200 || JudgeQuestions != 120 {
		t.Fatalf("budgets %d + %d, cap %d", JudgeQuestions, CheckQuestions, MaxJevQuestions)
	}
}

func TestJudgeStopsAtItsBudget(t *testing.T) {
	rep, p := judgeFixture()
	p.Facts = nil
	for i := range 250 {
		p.Facts = append(p.Facts, handin.ReportFact{Subject: "il6", Relation: "increases", Object: "infl",
			Quote: fmt.Sprintf("IL6 increases inflammation %d", i), SourceNode: "rec:a1"})
	}
	p.Links = []Span{{Start: 14, End: 17, Node: "rec:a1", Phrase: "IL6"}}
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	j, err := Judge(context.Background(), f, rep, p)
	if err != nil {
		t.Fatal(err)
	}
	if f.asks != JudgeQuestions || len(j.Facts) != JudgeQuestions || len(j.Links) != 0 {
		t.Fatalf("%d asks, %d facts, %d links; want %d, %d, 0", f.asks, len(j.Facts), len(j.Links), JudgeQuestions, JudgeQuestions)
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
	im := jev.Image{Format: "png", Data: []byte("fitted")}
	f := &fakeJev{p: func(string) float64 { return 0.2 }, choice: "contradicts", choiceP: 0.9}
	cs, err := CheckFindings(context.Background(), f, findingsFixture(), Evidence{Texts: texts, Images: map[string]jev.Image{"out:plot.png": im}})
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
	if fig.Question != "Does this figure show what the claim says?" || fig.Basis != "image" || fig.Status != "fail" || fig.P != 0.2 {
		t.Fatalf("figure %+v", fig)
	}
	if f.kinds[0] != "image" || len(f.images[0]) != 1 {
		t.Fatalf("the figure was asked as %s with images %v", f.kinds[0], f.images[0])
	}
	for ref, got := range f.images[0] {
		if string(got.Data) != "fitted" || !strings.Contains(f.states[0], ref) || !fencedContains(f.states[0], "IL6 rises") {
			t.Fatalf("image %s not named in state, or claim not fenced:\n%s", ref, f.states[0])
		}
	}
	if tab.Question != "Does this table's data match the claim?" || tab.Basis != "text" || tab.Status != "fail" || tab.Evidence != texts["out:data.csv"] {
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
			Evidence{Texts: map[string]string{"out:plot.png": "a plot", "paper:rec:p1": "abstract"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 2 || cs[0].Status != tc.fig || cs[0].Basis != "text" || cs[1].Status != tc.nv {
			t.Fatalf("p %v: %+v", tc.p, cs)
		}
	}
}

func TestCheckFindingsSkipsUntextedOutput(t *testing.T) {
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	cs, err := CheckFindings(context.Background(), f, findingsFixture(), Evidence{Texts: map[string]string{"out:data.csv": "a,b\n1,2\n"},
		Scripts: map[string]string{"out:data.csv": "write.csv(x)"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].CheckID != "table:out:data.csv" || f.asks != 1 {
		t.Fatalf("%d asks, checks %+v", f.asks, cs)
	}
}

func TestCheckFindingsJudgesUnseenFiguresFromTheirScript(t *testing.T) {
	rep := Report{
		Nodes: []Node{
			{ID: "out:fig.pdf", Kind: "output", ContentType: "application/pdf"},
			{ID: "out:none.svg", Kind: "output", ContentType: "image/svg+xml"},
			{ID: "rec:f1", Kind: "finding", Quote: "IL6 rises"},
		},
		Edges: []Edge{{From: "out:fig.pdf", To: "rec:f1", Kind: "supports"}, {From: "out:none.svg", To: "rec:f1", Kind: "supports"}},
	}
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	cs, err := CheckFindings(context.Background(), f, rep, Evidence{Scripts: map[string]string{"out:fig.pdf": "plot(il6); savefig('fig.pdf')"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || f.asks != 1 || cs[0].CheckID != "figure:out:fig.pdf" || cs[0].Basis != "script" ||
		cs[0].Question != "Does this figure, as the script that produced it describes it, show what the claim says?" || !fencedContains(f.states[0], "savefig") {
		t.Fatalf("%d asks, checks %+v", f.asks, cs)
	}
}

func TestCheckFindingsStopsAtItsBudgetFiguresFirst(t *testing.T) {
	rep := Report{}
	ev := Evidence{Texts: map[string]string{}}
	for i := range 49 {
		rep.Nodes = append(rep.Nodes, Node{ID: fmt.Sprintf("rec:p%d", i), Kind: "paper"})
		ev.Texts[fmt.Sprintf("paper:rec:p%d", i)] = "abstract"
	}
	for i := range 5 {
		f := Node{ID: fmt.Sprintf("rec:f%d", i), Kind: "finding", Quote: "claim"}
		out := Node{ID: fmt.Sprintf("out:t%d.csv", i), Kind: "output", ContentType: "text/csv"}
		rep.Nodes = append(rep.Nodes, out, f)
		rep.Edges = append(rep.Edges, Edge{From: out.ID, To: f.ID, Kind: "supports"})
		ev.Texts[out.ID] = "a,b\n1,2\n"
	}
	// 5 table checks and 5 × 49 novelty checks: 250 candidates.
	f := &fakeJev{p: func(string) float64 { return 0.9 }, choice: "confirms", choiceP: 0.9}
	cs, err := CheckFindings(context.Background(), f, rep, ev)
	if err != nil {
		t.Fatal(err)
	}
	if f.asks != CheckQuestions || len(cs) != CheckQuestions {
		t.Fatalf("%d asks, %d checks; want %d", f.asks, len(cs), CheckQuestions)
	}
	for i, k := range f.kinds {
		if (i < 5) != (k == "noul") {
			t.Fatalf("ask %d was %s; tables come before novelty", i, k)
		}
	}
}

func TestJudgeReadsConceptLabelsAndTypes(t *testing.T) {
	rep, p := judgeFixture()
	p.Concepts[0].Label = "Cures cancer"
	p.Facts = append(p.Facts[:1], handin.ReportFact{Subject: "tnf", Relation: "mentioned_in", Object: "rec:a2", Quote: "TNF", SourceNode: "rec:a2"})
	f := &fakeJev{p: func(string) float64 { return 0.9 }}
	if _, err := Judge(context.Background(), f, rep, p); err != nil {
		t.Fatal(err)
	}
	item, evidence, _ := strings.Cut(f.states[0], "Evidence 1:")
	if !fencedContains(item, "Cures cancer (protein) increases Inflammation (disease)") {
		t.Fatalf("the fact's item does not carry its concepts' labels and types:\n%s", f.states[0])
	}
	if strings.Contains(evidence, "protein") || strings.Contains(evidence, "disease") || strings.Contains(evidence, "Concept") {
		t.Fatalf("the model's concept types were fenced as evidence:\n%s", f.states[0])
	}
	item, evidence, _ = strings.Cut(f.states[1], "Evidence 1:")
	if !fencedContains(item, "TNF (protein) mentioned in arxiv:2\n") || strings.Contains(evidence, "protein") {
		t.Fatalf("a mentioned_in fact was not judged by its node's untyped label:\n%s", f.states[1])
	}
}

func TestCheckFindingsFallsBackWhenTheImageIsRefused(t *testing.T) {
	im := map[string]jev.Image{"out:plot.png": {Format: "png", Data: []byte("fitted")}}
	refused := fmt.Errorf("asking Jev: %w", jev.ErrRefused)
	f := &fakeJev{p: func(string) float64 { return 0.9 }, imgErr: refused}
	cs, err := CheckFindings(context.Background(), f, findingsFixture(), Evidence{Images: im,
		Texts: map[string]string{"out:plot.png": "A bar plot of IL6.", "out:data.csv": "a,b\n1,2\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].CheckID != "figure:out:plot.png" || cs[0].Basis != "text" || cs[1].CheckID != "table:out:data.csv" {
		t.Fatalf("checks %+v", cs)
	}
	f = &fakeJev{p: func(string) float64 { return 0.9 }, imgErr: refused}
	cs, err = CheckFindings(context.Background(), f, findingsFixture(), Evidence{Images: im, Texts: map[string]string{"out:data.csv": "a,b\n1,2\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].CheckID != "table:out:data.csv" {
		t.Fatalf("a refused image with nothing else was checked: %+v", cs)
	}
	f = &fakeJev{p: func(string) float64 { return 0.9 }, imgErr: errors.New("jev down")}
	if _, err := CheckFindings(context.Background(), f, findingsFixture(), Evidence{Images: im}); err == nil {
		t.Fatal("an unavailable Jev did not fail the pass")
	}
}

func TestCheckFindingsStatusMatchesStageReview(t *testing.T) {
	f := &fakeJev{p: func(string) float64 { return 0.32 }}
	cs, err := CheckFindings(context.Background(), f, findingsFixture(), Evidence{Texts: map[string]string{"out:data.csv": "a,b\n1,2\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Status != review.StatusOf(0.32) {
		t.Fatalf("p 0.32 gave %+v; stage review says %s", cs, review.StatusOf(0.32))
	}
}
