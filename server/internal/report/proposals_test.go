package report

import (
	"strings"
	"testing"

	"lykeion/server/internal/handin"
)

func repWith(quote, text string) Report {
	r := Report{Nodes: []Node{{ID: "question", Kind: "question", Quote: quote, Label: "Q"}, {ID: "rec:1", Kind: "paper", Label: "L"}}}
	if text != "" {
		r.HandIn = &HandIn{Text: text}
	}
	return r
}

func hin(facts []handin.ReportFact, links []handin.ReportLink) handin.ReportHandIn {
	return handin.ReportHandIn{Status: "done",
		Concepts: []handin.ReportConcept{{Key: "a", Label: "A", Type: "gene"}, {Key: "b", Label: "B", Type: "disease"}, {Key: "z", Label: "Z", Type: "other"}},
		Facts:    facts, Links: links}
}

func f(q, node string) handin.ReportFact {
	return handin.ReportFact{Subject: "a", Relation: "increases", Object: "b", Quote: q, SourceNode: node}
}

func TestGroundKeepsVerbatimQuote(t *testing.T) {
	p := Ground(repWith("So BRCA1 increases\n risk.", ""), hin([]handin.ReportFact{f("brca1  INCREASES risk", "question")}, nil))
	if len(p.Facts) != 1 || len(p.Concepts) != 2 {
		t.Fatalf("%+v", p)
	}
	if p.Facts[0].Quote != "BRCA1 increases\n risk" {
		t.Fatalf("kept quote %q, not the source's own text", p.Facts[0].Quote)
	}
}

func TestGroundDropsParaphrasedQuote(t *testing.T) {
	p := Ground(repWith("BRCA1 increases risk", ""), hin([]handin.ReportFact{f("BRCA1 raises risk", "question")}, nil))
	if len(p.Facts) != 0 || len(p.Concepts) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestGroundDropsUnknownNodeAndConcept(t *testing.T) {
	bad := f("risk", "nope")
	unknownSubject := f("risk", "question")
	unknownSubject.Subject = "ghost"
	unknownObject := f("risk", "question")
	unknownObject.Object = "ghost"
	badMention := handin.ReportFact{Subject: "a", Relation: "mentioned_in", Object: "ghost", Quote: "risk", SourceNode: "question"}
	goodMention := handin.ReportFact{Subject: "a", Relation: "mentioned_in", Object: "rec:1", Quote: "risk", SourceNode: "question"}
	p := Ground(repWith("risk", ""), hin([]handin.ReportFact{bad, unknownSubject, unknownObject, badMention, goodMention}, nil))
	if len(p.Facts) != 1 || p.Facts[0].Object != "rec:1" || len(p.Concepts) != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestGroundDropsConceptWithoutFacts(t *testing.T) {
	p := Ground(repWith("risk", ""), hin([]handin.ReportFact{f("risk", "question")}, nil))
	for _, c := range p.Concepts {
		if c.Key == "z" {
			t.Fatal("z has no fact")
		}
	}
}

func TestGroundLinksOccurrenceAndOverlap(t *testing.T) {
	text := "the drug X and drug X"
	p := Ground(repWith("", text), hin(nil, []handin.ReportLink{{Phrase: "drug X", Occurrence: 2, Node: "rec:1"}}))
	if len(p.Links) != 1 || text[p.Links[0].Start:p.Links[0].End] != "drug X" || p.Links[0].Start != 15 {
		t.Fatalf("%+v", p.Links)
	}
	p = Ground(repWith("", text), hin(nil, []handin.ReportLink{
		{Phrase: "drug X and", Occurrence: 1, Node: "rec:1"}, {Phrase: "the drug X", Occurrence: 1, Node: "rec:1"}, {Phrase: "drug X", Occurrence: 2, Node: "rec:1"},
		{Phrase: "drug X", Occurrence: 3, Node: "rec:1"}, {Phrase: "drug x", Occurrence: 1, Node: "rec:1"}, {Phrase: "drug X", Occurrence: 1, Node: "ghost"}}))
	// "the drug X" starts first, so the overlapping "drug X and" is dropped.
	if len(p.Links) != 2 || p.Links[0].Start != 0 || p.Links[1].Start != 15 {
		t.Fatalf("%+v", p.Links)
	}
}

func TestGroundKeepsOnlyLinksToGroundingSources(t *testing.T) {
	rep := repWith("", "the drug X raises Y in mice")
	rep.Nodes = append(rep.Nodes, Node{ID: "rec:h", Kind: "hypothesis", Label: "H"}, Node{ID: "rec:d", Kind: "dataset", Label: "GSE1"})
	p := Ground(rep, hin(nil, []handin.ReportLink{
		{Phrase: "the drug X", Occurrence: 1, Node: "question"}, {Phrase: "raises Y", Occurrence: 1, Node: "rec:h"},
		{Phrase: "in mice", Occurrence: 1, Node: "rec:1"}, {Phrase: "drug X", Occurrence: 1, Node: "rec:d"}}))
	if len(p.Links) != 2 || p.Links[0].Node != "rec:d" || p.Links[1].Node != "rec:1" {
		t.Fatalf("links %+v, want only the dataset's and the paper's", p.Links)
	}
}

func TestGroundLinksOnlyPlainProse(t *testing.T) {
	cases := []struct {
		name, text, phrase string
		keep               bool
	}{
		{"plain", "Plain Smith 2020 here.", "Smith 2020", true},
		{"inside bold", "See **Smith 2020** here.", "Smith 2020", true},
		{"less than", "Significant at p < 0.05 overall.", "p < 0.05", true},
		{"underscore inside a word", "We used TCGA_BRCA data.", "TCGA_BRCA", true},
		{"after an escaped backtick", "A \\` mark, then Smith 2020 here.", "Smith 2020", true},
		{"lazy continuation line", "Intro line\n    Smith 2020 continues.", "Smith 2020", true},
		{"crosses bold marks", "See **Smith** 2020 here.", "**Smith** 2020", false},
		{"code span", "See `Smith 2020` here.", "Smith 2020", false},
		{"one plain copy, one code copy", "Smith 2020 found it; see `Smith 2020`.", "Smith 2020", false},
		{"code span across a soft break", "See `code\nSmith 2020` here.", "Smith 2020", false},
		{"fenced block", "Before.\n```\nSmith 2020\n```\nAfter.", "Smith 2020", false},
		{"tilde fence", "Before.\n~~~\nSmith 2020\n~~~\nAfter.", "Smith 2020", false},
		{"nested longer fence", "Before.\n````\n```\nSmith 2020\n```\n````\nAfter.", "Smith 2020", false},
		{"indented code block", "Intro.\n\n    Smith 2020\n\nAfter.", "Smith 2020", false},
		{"link text", "See [Smith 2020](https://x.org/a).", "Smith 2020", false},
		{"link destination", "See [the paper](#smith2020) now.", "smith2020", false},
		{"url", "Read https://smith2020.example/x today.", "smith2020", false},
		{"www", "Read www.smith2020.example today.", "smith2020", false},
		{"email", "Mail smith2020@lab.example now.", "smith2020", false},
		{"doi with prefix", "Cited as doi:10.1038/smith2020 here.", "smith2020", false},
		{"bare doi", "Cited as 10.1038/smith2020 here.", "smith2020", false},
		{"bare domain", "Data at smith2020.org today.", "smith2020", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Ground(repWith("", c.text), hin(nil, []handin.ReportLink{{Phrase: c.phrase, Occurrence: 1, Node: "rec:1"}}))
			if !c.keep {
				if len(p.Links) != 0 {
					t.Fatalf("kept %+v in %q, want it dropped", p.Links, c.text)
				}
				return
			}
			at := strings.Index(c.text, c.phrase)
			if at < 0 {
				t.Fatalf("bad case: %q not in %q", c.phrase, c.text)
			}
			if len(p.Links) != 1 || p.Links[0].Start != at {
				t.Fatalf("links %+v in %q, want one at %d", p.Links, c.text, at)
			}
		})
	}
}

func TestGroundNoHandInNoLinks(t *testing.T) {
	p := Ground(repWith("", ""), hin(nil, []handin.ReportLink{{Phrase: "x", Occurrence: 1, Node: "rec:1"}}))
	if len(p.Links) != 0 {
		t.Fatalf("%+v", p)
	}
}
