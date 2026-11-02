package review

import (
	"strings"
	"testing"

	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
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
		{"withdrawn in validation", KindWithdrawn, board.Validation, `{"item":"r3","reason":"r"}`, true},
		{"assessment ok", KindAssessment, board.Validation, `{"hypothesis":"h","measurement":"slope","control":null,"test":{"kind":"t","statistic":2.2,"stat_digits":1,"df1":28,"df2":0,"p":0.03,"p_digits":2,"p_bound":"="},"observed":null}`, true},
		{"assessment no measurement", KindAssessment, board.Validation, `{"hypothesis":"h","measurement":""}`, false},
		{"verdict ok", KindVerdict, board.Validation, `{"claim":"c","verdict":"refuted","source":"data.csv","quote":"q"}`, true},
		{"verdict bad", KindVerdict, board.Validation, `{"claim":"c","verdict":"maybe","source":"data.csv","quote":"q"}`, false},
		{"verdict no source", KindVerdict, board.Validation, `{"claim":"c","verdict":"supported","source":"","quote":"q"}`, false},
		{"flagged ok", KindFlaggedError, board.Validation, `{"source":"s","quote":"q","problem":"p"}`, true},
		{"flagged no problem", KindFlaggedError, board.Validation, `{"source":"s","quote":"q","problem":""}`, false},
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
	for _, kind := range []string{KindAssessment, KindVerdict, KindFlaggedError} {
		if got, ok := ColumnOf(kind); !ok || got != board.Validation {
			t.Errorf("ColumnOf(%s)=%s,%v", kind, got, ok)
		}
		if Validate(kind, board.Experimentation, []byte(`{}`)) == nil {
			t.Errorf("%s accepted in experimentation", kind)
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

func TestValidationItemIdentities(t *testing.T) {
	r := func(kind, body string) db.Record { return db.Record{Kind: kind, Body: []byte(body)} }
	a1 := r(KindAssessment, `{"hypothesis":" Slope  rises ","measurement":"m"}`)
	a2 := r(KindAssessment, `{"hypothesis":"Slope falls","measurement":"m"}`)
	if IdentityOf(a1) != "slope rises" || IdentityOf(a1) == IdentityOf(a2) || LabelOf(a1) != "Slope  rises" {
		t.Errorf("assessments: %q %q %q", IdentityOf(a1), IdentityOf(a2), LabelOf(a1))
	}
	v := r(KindVerdict, `{"claim":"A  Claim","verdict":"supported","source":"s","quote":"q"}`)
	if IdentityOf(v) != "a claim" || LabelOf(v) != "A  Claim" {
		t.Errorf("verdict: %q %q", IdentityOf(v), LabelOf(v))
	}
	f1 := r(KindFlaggedError, `{"source":"doi:10.1/A","quote":"The  quote","problem":"p"}`)
	f2 := r(KindFlaggedError, `{"source":"doi:10.1/A","quote":"another","problem":"p"}`)
	if IdentityOf(f1) != "doi:10.1/a\nthe quote" || IdentityOf(f1) == IdentityOf(f2) || LabelOf(f1) != "doi:10.1/A: The  quote" {
		t.Errorf("flagged: %q %q %q", IdentityOf(f1), IdentityOf(f2), LabelOf(f1))
	}
}
