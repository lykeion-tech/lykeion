package review

import (
	"strings"
	"testing"

	"lykeion/server/internal/board"
)

func TestValidateItems(t *testing.T) {
	long := strings.Repeat("x", 2001)
	hyp := func(pred, ctl, cites string) string {
		return `{"text":"t","prediction":` + pred + `,"control":` + ctl + `,"cites":` + cites + `}`
	}
	dir := `{"shape":"direction","direction":"increase"}`
	ctl := `{"text":"baseline"}`
	cases := []struct {
		name, kind, col, body string
		ok                    bool
	}{
		{"paper arxiv", KindPaper, board.Grounding, `{"id":"arxiv:2106.01234","text":"t"}`, true},
		{"paper old arxiv", KindPaper, board.Grounding, `{"id":"arxiv:hep-th/9901001","text":"t"}`, true},
		{"paper doi", KindPaper, board.Grounding, `{"id":"doi:10.1000/abc","text":"t"}`, true},
		{"paper pmid", KindPaper, board.Grounding, `{"id":"pmid:12345","text":"t"}`, true},
		{"paper bad id", KindPaper, board.Grounding, `{"id":"12345","text":"t"}`, false},
		{"paper empty text", KindPaper, board.Grounding, `{"id":"pmid:1","text":""}`, false},
		{"paper long text", KindPaper, board.Grounding, `{"id":"pmid:1","text":"` + long + `"}`, false},
		{"dataset ok", KindDataset, board.Grounding, `{"accession":"GSE12345","text":"t"}`, true},
		{"dataset no accession", KindDataset, board.Grounding, `{"accession":"","text":"t"}`, false},
		{"dataset long accession", KindDataset, board.Grounding, `{"accession":"` + strings.Repeat("a", 201) + `","text":"t"}`, false},
		{"no qualifying ok", KindNoQualifying, board.Grounding, `{"text":"none"}`, true},
		{"no qualifying empty", KindNoQualifying, board.Grounding, `{"text":""}`, false},
		{"hyp direction", KindHypothesis, board.Hypotheses, hyp(dir, ctl, `["a"]`), true},
		{"hyp direction missing", KindHypothesis, board.Hypotheses, hyp(`{"shape":"direction"}`, ctl, `["a"]`), false},
		{"hyp outcomes", KindHypothesis, board.Hypotheses, hyp(`{"shape":"outcomes","outcomes":["a","b"],"chosen":"a"}`, ctl, `["a"]`), true},
		{"hyp one outcome", KindHypothesis, board.Hypotheses, hyp(`{"shape":"outcomes","outcomes":["a"],"chosen":"a"}`, ctl, `["a"]`), false},
		{"hyp chosen outside", KindHypothesis, board.Hypotheses, hyp(`{"shape":"outcomes","outcomes":["a","b"],"chosen":"c"}`, ctl, `["a"]`), false},
		{"hyp range", KindHypothesis, board.Hypotheses, hyp(`{"shape":"range","low":1,"high":2,"uncertainty":"u"}`, ctl, `["a"]`), true},
		{"hyp range inverted", KindHypothesis, board.Hypotheses, hyp(`{"shape":"range","low":2,"high":1,"uncertainty":"u"}`, ctl, `["a"]`), false},
		{"hyp range no uncertainty", KindHypothesis, board.Hypotheses, hyp(`{"shape":"range","low":1,"high":2}`, ctl, `["a"]`), false},
		{"hyp unknown shape", KindHypothesis, board.Hypotheses, hyp(`{"shape":"x"}`, ctl, `["a"]`), false},
		{"hyp control none", KindHypothesis, board.Hypotheses, hyp(dir, `{"none":"nothing to compare"}`, `["a"]`), true},
		{"hyp control both", KindHypothesis, board.Hypotheses, hyp(dir, `{"text":"a","none":"b"}`, `["a"]`), false},
		{"hyp control neither", KindHypothesis, board.Hypotheses, hyp(dir, `{}`, `["a"]`), false},
		{"hyp no cites", KindHypothesis, board.Hypotheses, hyp(dir, ctl, `[]`), false},
		{"outputs ok", KindOutputs, board.Experimentation, `{"files":["a.csv"]}`, true},
		{"outputs none", KindOutputs, board.Experimentation, `{"files":[]}`, false},
		{"outputs too many", KindOutputs, board.Experimentation, `{"files":["` + strings.Repeat(`a","`, 50) + `a"]}`, false},
		{"not an item", "note", board.Grounding, `{"text":"x"}`, true},
		{"withdrawn ok", KindWithdrawn, board.Grounding, `{"item":"r3","reason":"it does not resolve"}`, true},
		{"withdrawn in hypotheses", KindWithdrawn, board.Hypotheses, `{"item":"r3","reason":"r"}`, true},
		{"withdrawn in experimentation", KindWithdrawn, board.Experimentation, `{"item":"r3","reason":"r"}`, true},
		{"withdrawn in validation", KindWithdrawn, board.Validation, `{"item":"r3","reason":"r"}`, false},
		{"withdrawn no key", KindWithdrawn, board.Grounding, `{"item":"arxiv:2106.01234","reason":"r"}`, false},
		{"withdrawn no reason", KindWithdrawn, board.Grounding, `{"item":"r3","reason":""}`, false},
		{"withdrawn long reason", KindWithdrawn, board.Grounding, `{"item":"r3","reason":"` + long + `"}`, false},
	}
	for _, c := range cases {
		err := Validate(c.kind, c.col, []byte(c.body))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestItemsOnlyInTheirColumn(t *testing.T) {
	err := Validate(KindHypothesis, board.Grounding, []byte(`{}`))
	if err == nil || err.Error() != "hypothesis records are written in the Hypotheses column" {
		t.Fatalf("got %v", err)
	}
	for kind, want := range map[string]string{KindPaper: board.Grounding, KindDataset: board.Grounding, KindNoQualifying: board.Grounding, KindHypothesis: board.Hypotheses, KindOutputs: board.Experimentation} {
		if got, ok := ColumnOf(kind); !ok || got != want {
			t.Errorf("ColumnOf(%s)=%s,%v", kind, got, ok)
		}
		if Validate(kind, board.Validation, []byte(`{}`)) == nil {
			t.Errorf("%s accepted in validation", kind)
		}
	}
	if _, ok := ColumnOf("note"); ok {
		t.Error("note is not an item")
	}
}

func TestClipCutsAtWordBoundaryWithEllipsis(t *testing.T) {
	short := "short label"
	if got := clip(short); got != short {
		t.Errorf("short label changed: %q", got)
	}
	long := strings.Repeat("word ", 20) // 100 characters
	if got, want := clip(long), strings.TrimSpace(strings.Repeat("word ", 16))+"…"; got != want {
		t.Errorf("word cut: %q, want %q", got, want)
	}
	if got, want := clip(strings.Repeat("é", 100)), strings.Repeat("é", 79)+"…"; got != want {
		t.Errorf("rune cut: %q", got)
	}
	if got := clip(strings.Repeat("ab ", 40)); len([]rune(got)) > 80 {
		t.Errorf("clip is %d characters", len([]rune(got)))
	}
}
