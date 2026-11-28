package handin

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
)

func parsed(t *testing.T, k Kind, raw string) HandIn {
	t.Helper()
	h, probs := Parse(k, []byte(raw))
	if len(probs) > 0 {
		t.Fatalf("parse %s: %v", raw, probs)
	}
	return h
}

func studyEvidence() Evidence {
	return NewEvidence([]db.Record{
		rec(board.Grounding, "call", "c1", map[string]any{"answer": "hits: doi:10.1001/a, 10.1001/b and GSE7"}),
		rec(board.Grounding, "search", "", map[string]any{"results": []string{"doi:10.9999/hosted"}}),
		rec(board.Grounding, "paper", "r2", map[string]any{"id": "doi:10.1001/a", "text": "t"}),
		rec(board.Experimentation, "output", "o1", map[string]any{"name": "fit.txt"}),
		rec(board.Experimentation, "run", "x1", map[string]any{"tool": "run_script", "path": "fit.py"}),
		rec(board.Experimentation, "run", "x2", map[string]any{"tool": "run_code", "path": "explore.py"}),
	})
}

func kinds(recs []NewRecord) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.Kind)
	}
	return out
}

func body(t *testing.T, r NewRecord) map[string]any {
	t.Helper()
	b, _ := json.Marshal(r.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestConvertGrounding(t *testing.T) {
	h := parsed(t, Grounding, `{"status":"done","answer":"Two papers.","reason":null,"replies":[],
		"papers":[{"id":"https://doi.org/10.1001/A","why":"meets 1"},{"id":"10.9999/hosted","why":"meets 2"}],
		"datasets":[{"accession":"GSE7","why":"data"}],"no_qualifying":null}`)
	recs, probs := Convert(Grounding, true, h, studyEvidence(), Review{})
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if !slices.Equal(kinds(recs), []string{"paper", "paper", "dataset", "summary"}) {
		t.Fatalf("kinds %v", kinds(recs))
	}
	p := body(t, recs[0])
	if p["id"] != "doi:10.1001/a" || p["text"] != "meets 1" || len(p["cites"].([]any)) != 1 || p["cites"].([]any)[0] != "call:c1" {
		t.Errorf("paper %v", p)
	}
	// Found only by the sources server's own search: found, but nothing to cite.
	if p := body(t, recs[1]); len(p["cites"].([]any)) != 0 {
		t.Errorf("hosted paper %v", p)
	}
	if d := body(t, recs[2]); d["accession"] != "GSE7" || d["cites"].([]any)[0] != "call:c1" {
		t.Errorf("dataset %v", d)
	}
	if s := body(t, recs[3]); s["text"] != "Two papers." || len(s["cites"].([]any)) != 0 {
		t.Errorf("summary %v", s)
	}
}

func TestConvertMemberAndCouldNotDoWriteNothing(t *testing.T) {
	h := parsed(t, Grounding, `{"status":"done","answer":"a","reason":null,"replies":[],
		"papers":[{"id":"10.5/nowhere","why":"w"}],"datasets":[],"no_qualifying":null}`)
	if recs, probs := Convert(Grounding, false, h, studyEvidence(), Review{}); recs != nil || probs != nil {
		t.Errorf("member: %v %v", recs, probs)
	}
	h = parsed(t, Grounding, `{"status":"could_not_do","answer":"a","reason":"no access","replies":[],
		"papers":[],"datasets":[],"no_qualifying":null}`)
	if recs, probs := Convert(Grounding, true, h, studyEvidence(), Review{}); recs != nil || probs != nil {
		t.Errorf("could_not_do: %v %v", recs, probs)
	}
}

func TestConvertProblems(t *testing.T) {
	prev := Review{Failed: map[string]string{"doi:10.1001/a": "r4"}, Missed: []string{"doi:10.1001/b"}}
	cases := []struct {
		name string
		k    Kind
		raw  string
		want []string
	}{
		{"not found", Grounding, `{"status":"done","answer":"a","reason":null,"replies":[],
			"papers":[{"id":"doi:10.1001/a","why":"w"},{"id":"10.1000/xyz","why":"w"}],"datasets":[{"accession":"GSE9","why":"w"}],"no_qualifying":null}`,
			[]string{"paper 2: 10.1000/xyz was not found through Lykeion's sources", "dataset 1: GSE9 was not found through Lykeion's sources"}},
		{"empty grounding", Grounding, `{"status":"done","answer":"a","reason":null,"replies":[],"papers":[],"datasets":[],"no_qualifying":null}`,
			[]string{"a done Grounding hand-in needs at least one paper, dataset or no_qualifying"}},
		{"empty experimentation", Experimentation, `{"status":"done","answer":"a","reason":null,"replies":[],"outputs":[],"values":[]}`,
			[]string{"a done Experimentation hand-in needs at least one value or output"}},
		{"outputs and values", Experimentation, `{"status":"done","answer":"a","reason":null,"replies":[],"outputs":["fit.txt","gone.csv"],
			"values":[{"name":"a","value":"1","file":"fit.txt"},{"name":"b","value":"2","file":"fit.py"},{"name":"c","value":"3","file":"explore.py"},{"name":"d","value":"4","file":""}]}`,
			[]string{"output 2: gone.csv was not saved in this study",
				"value 3: file explore.py names neither a saved output nor a script run recorded in this study",
				"value 4: name the saved output or script run that shows the value"}},
		{"hypothesis cites", Hypotheses, `{"status":"done","answer":"a","reason":null,"replies":[],"hypotheses":[
			{"text":"t1","prediction_shape":"direction","direction":"increase","outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,"control":"c","no_control":null,"cites":["https://doi.org/10.1001/A","10.1001/b"]},
			{"text":"t2","prediction_shape":"direction","direction":"increase","outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,"control":"c","no_control":null,"cites":["10.8888/made-up"]},
			{"text":"t3","prediction_shape":"direction","direction":"increase","outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,"control":"c","no_control":null,"cites":["10.9999/hosted"]}]}`,
			[]string{"hypothesis 2: cite 10.8888/made-up is neither a Grounding paper or dataset, a file attached to the study, nor a source recorded in this study",
				"hypothesis 3: cite a Grounding paper or dataset of this study, a file attached to it, or a source you looked up through Lykeion's tools"}},
		{"replies", Grounding, `{"status":"done","answer":"a","reason":null,"papers":[{"id":"10.1001/a","why":"w"}],"datasets":[],"no_qualifying":null,"replies":[
			{"item":"doi:10.1001/b","action":"corrected","reason":"r"},
			{"item":"doi:10.1001/a","action":"declined","reason":"r"},
			{"item":"10.1001/b","action":"declined","reason":"r"},
			{"item":"doi:10.7777/never","action":"withdrawn","reason":"r"},
			{"item":"https://doi.org/10.1001/A","action":"corrected","reason":"r"}]}`,
			[]string{"reply 1: corrected, but no item doi:10.1001/b is in this hand-in",
				"reply 2: only a missed paper the review sent back can be declined",
				"reply 4: doi:10.7777/never was not sent back by the last review, so it cannot be withdrawn"}},
		{"autoresearch", Autoresearch, `{"status":"done","answer":"a","reason":null,"report":null,"new_studies":[]}`,
			[]string{"not yet supported"}},
	}
	for _, c := range cases {
		h := parsed(t, c.k, c.raw)
		// A review sends back items of its own column: only the replies case answers one.
		p := Review{}
		if c.name == "replies" {
			p = prev
		}
		recs, probs := Convert(c.k, true, h, studyEvidence(), p)
		if !slices.Equal(probs, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, probs, c.want)
		}
		if recs != nil {
			t.Errorf("%s: records %v", c.name, kinds(recs))
		}
	}
	// A review rule run on a converted body: a paper id of no known form.
	h := parsed(t, Grounding, `{"status":"done","answer":"a","reason":null,"replies":[],"papers":[{"id":"hits:","why":"w"}],"datasets":[],"no_qualifying":null}`)
	if _, probs := Convert(Grounding, true, h, studyEvidence(), Review{}); !slices.Equal(probs, []string{"paper 1: id must be arxiv:…, doi:10.… or pmid:…"}) {
		t.Errorf("item rule: %q", probs)
	}
}

func TestConvertReplies(t *testing.T) {
	prev := Review{Failed: map[string]string{"doi:10.1001/a": "r4"}, Missed: []string{"doi:10.1001/b"}}
	h := parsed(t, Grounding, `{"status":"done","answer":"a","reason":null,"datasets":[],"no_qualifying":null,
		"papers":[{"id":"10.1001/b","why":"w"}],"replies":[
		{"item":"https://doi.org/10.1001/A","action":"withdrawn","reason":"off topic"},
		{"item":"https://doi.org/10.1001/B","action":"declined","reason":"not relevant"}]}`)
	recs, probs := Convert(Grounding, true, h, studyEvidence(), prev)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if !slices.Equal(kinds(recs), []string{"paper", "withdrawn", "declined", "summary"}) {
		t.Fatalf("kinds %v", kinds(recs))
	}
	if w := body(t, recs[1]); w["item"] != "r4" || w["reason"] != "off topic" {
		t.Errorf("withdrawn %v", w)
	}
	if d := body(t, recs[2]); d["item"] != "doi:10.1001/b" || d["reason"] != "not relevant" {
		t.Errorf("declined %v", d)
	}
	// A hypothesis sent back is named by its text, in any spacing and case.
	h = parsed(t, Hypotheses, `{"status":"done","answer":"a","reason":null,"hypotheses":[],"replies":[
		{"item":"A  stated\nEffect","action":"withdrawn","reason":"weak"}]}`)
	recs, probs = Convert(Hypotheses, true, h, studyEvidence(), Review{Failed: map[string]string{"a stated effect": "r6"}})
	if len(probs) > 0 || len(recs) != 2 || body(t, recs[0])["item"] != "r6" {
		t.Fatalf("hypothesis withdrawn: %v %v", recs, probs)
	}
}

func TestConvertExperimentation(t *testing.T) {
	h := parsed(t, Experimentation, `{"status":"done","answer":"a","reason":null,"replies":[],"outputs":["fit.txt"],
		"values":[{"name":"slope","value":"0.42","file":"fit.txt"},{"name":"n","value":"12","file":"fit.py"}]}`)
	recs, probs := Convert(Experimentation, true, h, studyEvidence(), Review{})
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if !slices.Equal(kinds(recs), []string{"outputs", "value", "value", "summary"}) {
		t.Fatalf("kinds %v", kinds(recs))
	}
	if o := body(t, recs[0]); o["files"].([]any)[0] != "fit.txt" {
		t.Errorf("outputs %v", o)
	}
	if v := body(t, recs[1]); v["name"] != "slope" || v["value"] != "0.42" || v["file"] != "fit.txt" || len(v["cites"].([]any)) != 0 {
		t.Errorf("value from a file %v", v)
	}
	if v := body(t, recs[2]); v["file"] != "fit.py" || v["cites"].([]any)[0] != "run:x1" {
		t.Errorf("value from a script %v", v)
	}
}

func TestCorrectedValueReplyNamesThePriorItem(t *testing.T) {
	prev := Review{Failed: map[string]string{"slope": "r1", "0.42: slope": "r1"}}
	for _, name := range []string{" SLOPE ", "0.42: slope"} {
		h := HandIn{Status: "done", Answer: "corrected", Values: []Value{{Name: "Slope", Value: "0.43", File: "fit.py"}}, Replies: []Reply{{Item: name, Action: "corrected", Reason: "Recalculated."}}}
		recs, problems := Convert(Experimentation, true, h, studyEvidence(), prev)
		if len(problems) != 0 {
			t.Fatalf("reply %q: %v", name, problems)
		}
		if got := body(t, recs[0])["name"]; got != "Slope" {
			t.Fatalf("reply %q value name = %v", name, got)
		}
	}
	// A new, unrelated value must not answer the failed item.
	h := HandIn{Status: "done", Answer: "new value", Values: []Value{{Name: "Other", Value: "0.43", File: "fit.py"}}, Replies: []Reply{{Item: "slope", Action: "corrected", Reason: "Replaced."}}}
	if _, problems := Convert(Experimentation, true, h, studyEvidence(), prev); len(problems) == 0 {
		t.Fatal("unrelated value accepted as a correction")
	}
	h = HandIn{Status: "done", Answer: "conflicting replies", Values: []Value{{Name: "Slope", Value: "0.43", File: "fit.py"}}, Replies: []Reply{
		{Item: "slope", Action: "corrected", Reason: "Recalculated."},
		{Item: "0.42: slope", Action: "withdrawn", Reason: "Removed."},
	}}
	if _, problems := Convert(Experimentation, true, h, studyEvidence(), prev); len(problems) == 0 {
		t.Fatal("conflicting replies to the same value were accepted")
	}
	h.Replies = h.Replies[:1]
	if _, problems := Convert(Experimentation, true, h, studyEvidence(), Review{Failed: map[string]string{"slope": "r1", " SLOPE ": "r2"}}); len(problems) == 0 {
		t.Fatal("an ambiguous name was accepted as a correction")
	}
}

func TestExperimentationValueMayReferenceAnExactRecordedScriptRun(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Experimentation, "run", "x1", map[string]any{"tool": "run_script", "path": "readiness.py", "exit_status": 0}),
		rec(board.Experimentation, "run", "x2", map[string]any{"tool": "run_script", "path": "readiness.py", "exit_status": 0}),
	})
	h := parsed(t, Experimentation, `{"status":"done","answer":"4","reason":null,"replies":[],"outputs":[],"values":[{"name":"sum","value":"4","file":"run:x1"}]}`)
	recs, probs := Convert(Experimentation, true, h, e, Review{})
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if v := body(t, recs[0]); v["file"] != "readiness.py" || v["script_run"] != true || !slices.Equal(v["cites"].([]any), []any{"run:x1"}) {
		t.Fatalf("value has wrong script provenance: %v", v)
	}
}

func TestExperimentationRejectsUnusableScriptRunReferences(t *testing.T) {
	gone := time.Now()
	superseded := rec(board.Experimentation, "run", "old", map[string]any{"tool": "run_script", "path": "old.py"})
	superseded.SupersededAt = &gone
	malformed := rec(board.Experimentation, "run", "invalid", nil)
	malformed.Body = []byte(`{"tool":`)
	e := NewEvidence([]db.Record{
		superseded,
		rec(board.Grounding, "run", "othercolumn", map[string]any{"tool": "run_script", "path": "other.py"}),
		rec(board.Experimentation, "run", "code", map[string]any{"tool": "run_code", "path": "explore.py"}),
		rec(board.Experimentation, "run", "nopath", map[string]any{"tool": "run_script"}),
		malformed,
	})
	for _, ref := range []string{"run:missing", "run:old", "run:othercolumn", "run:code", "run:nopath", "run:invalid"} {
		t.Run(ref, func(t *testing.T) {
			h := parsed(t, Experimentation, `{"status":"done","answer":"4","reason":null,"replies":[],"outputs":[],"values":[{"name":"sum","value":"4","file":"`+ref+`"}]}`)
			recs, probs := Convert(Experimentation, true, h, e, Review{})
			if recs != nil || len(probs) != 1 {
				t.Fatalf("got records %v, problems %v", recs, probs)
			}
		})
	}
}

func TestExperimentationPreservesSavedOutputNamedLikeRunReference(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Experimentation, "output", "o1", map[string]any{"name": "run:x1"}),
		rec(board.Experimentation, "run", "x1", map[string]any{"tool": "run_script", "path": "readiness.py"}),
	})
	h := parsed(t, Experimentation, `{"status":"done","answer":"4","reason":null,"replies":[],"outputs":[],"values":[{"name":"sum","value":"4","file":"run:x1"}]}`)
	recs, probs := Convert(Experimentation, true, h, e, Review{})
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if v := body(t, recs[0]); v["file"] != "run:x1" || len(v["cites"].([]any)) != 0 {
		t.Fatalf("saved output was not preserved: %v", v)
	}
}

func TestConvertAudit(t *testing.T) {
	h := parsed(t, Auditor, `{"status":"done","answer":"Mostly sound.","reason":null,"replies":[],
		"concerns":[{"item":"doi:10.1001/a","concern":"the quote is not in the call"}]}`)
	recs, probs := Convert(Auditor, false, h, studyEvidence(), Review{})
	if len(probs) > 0 || !slices.Equal(kinds(recs), []string{"audit"}) {
		t.Fatalf("%v %v", kinds(recs), probs)
	}
	a := body(t, recs[0])
	if a["text"] != "Mostly sound." || a["findings"].([]any)[0] != "doi:10.1001/a: the quote is not in the call" {
		t.Errorf("audit %v", a)
	}
}

func validationEvidence(mode string) Evidence {
	e := NewEvidence([]db.Record{
		rec(board.Grounding, "call", "c1", map[string]any{"answer": "hits: doi:10.1001/a"}),
		rec(board.Grounding, "call", "c2", map[string]any{"server": "attachments", "args": map[string]any{"name": "data.csv"}, "answer": "x,y"}),
		rec(board.Hypotheses, "hypothesis", "r3", map[string]any{"text": "Slope rises with dose"}),
		rec(board.Experimentation, "value", "r5", map[string]any{"name": "slope", "value": "0.4"}),
		rec(board.Experimentation, "value", "r6", map[string]any{"name": "baseline", "value": "0.1"}),
	})
	e.Mode = mode
	return e
}

const (
	assessOK   = `{"hypothesis":" Slope rises with dose ","measurement":"slope","control":"baseline","observed":null,"test":{"kind":"t","statistic":2.31,"df1":28,"df2":null,"p":0.03,"p_bound":"="}}`
	verdictOK  = `{"claim":"c","verdict":"supported","source":"data.csv","quote":"x,y"}`
	flaggedOK  = `{"source":"https://doi.org/10.1001/A","quote":"q","problem":"p"}`
	validEmpty = `"status":"done","answer":"a","reason":null,"replies":[]`
)

func vHandIn(t *testing.T, as, vs, fs string) HandIn {
	return parsed(t, Validation, `{`+validEmpty+`,"assessments":[`+as+`],"verdicts":[`+vs+`],"flagged_errors":[`+fs+`]}`)
}

func TestConvertModeRules(t *testing.T) {
	cases := []struct {
		name, mode, as, vs, fs string
		want                   []string
	}{
		{"verdicts in runs mode", "runs", assessOK, verdictOK, "", []string{"this study has recorded runs: hand in assessments, not verdicts"}},
		{"flagged in runs mode", "runs", assessOK, "", flaggedOK, []string{"this study has recorded runs: hand in assessments, not verdicts"}},
		{"assessments in claims mode", "claims", assessOK, verdictOK, "", []string{"this study has no recorded runs: hand in verdicts, not assessments"}},
		{"unknown hypothesis", "runs", `{"hypothesis":"Slope falls","measurement":"slope","control":null,"observed":null,"test":null}`, "", "",
			[]string{"assessment 1: hypothesis Slope falls matches no hypothesis of this study", "hypothesis \"Slope rises with dose\" has no assessment"}},
		{"unknown measurement", "runs", `{"hypothesis":"Slope rises with dose","measurement":"intercept","control":"none","observed":null,"test":null}`, "", "",
			[]string{"assessment 1: measurement intercept matches no recorded value of this study", "assessment 1: control none matches no recorded value of this study"}},
		{"observed in the lead's own words", "runs", `{"hypothesis":"Slope rises with dose","measurement":"slope","control":null,"observed":"it rose","test":null}`, "", "",
			[]string{"assessment 1: observed it rose matches no recorded value of this study"}},
		{"done empty runs", "runs", "", "", "", []string{"a done Validation hand-in needs at least one assessment"}},
		{"done empty claims", "claims", "", "", "", []string{"a done Validation hand-in needs at least one verdict or flagged error"}},
	}
	for _, c := range cases {
		recs, probs := Convert(Validation, true, vHandIn(t, c.as, c.vs, c.fs), validationEvidence(c.mode), Review{})
		if !slices.Equal(probs, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, probs, c.want)
		}
		if recs != nil {
			t.Errorf("%s: records %v", c.name, kinds(recs))
		}
	}
	h := parsed(t, Validation, `{"status":"could_not_do","answer":"a","reason":"no runs to check","replies":[],"assessments":[],"verdicts":[],"flagged_errors":[]}`)
	if recs, probs := Convert(Validation, true, h, validationEvidence("runs"), Review{}); recs != nil || probs != nil {
		t.Errorf("could_not_do: %v %v", recs, probs)
	}

	recs, probs := Convert(Validation, true, vHandIn(t, assessOK, "", ""), validationEvidence("runs"), Review{})
	if len(probs) > 0 || !slices.Equal(kinds(recs), []string{"assessment", "summary"}) {
		t.Fatalf("runs: %v %v", kinds(recs), probs)
	}
	a := body(t, recs[0])
	var tst map[string]any
	tst = a["test"].(map[string]any)
	if a["measurement"] != "slope" || a["control"] != "baseline" || tst["stat_digits"] != 2.0 || tst["p_digits"] != 2.0 || tst["df1"] != 28.0 {
		t.Errorf("assessment %v", a)
	}
	recs, probs = Convert(Validation, true, vHandIn(t, "", verdictOK, flaggedOK), validationEvidence("claims"), Review{})
	if len(probs) > 0 || !slices.Equal(kinds(recs), []string{"verdict", "flagged_error", "summary"}) {
		t.Fatalf("claims: %v %v", kinds(recs), probs)
	}
	if f := body(t, recs[1]); f["source"] != "doi:10.1001/a" || f["problem"] != "p" {
		t.Errorf("flagged %v", f)
	}
}

func TestVerdictSourceMustBeKnown(t *testing.T) {
	vs := verdictOK + `,{"claim":"c","verdict":"refuted","source":"made-up.pdf","quote":"q"},{"claim":"c","verdict":"not_enough_info","source":"10.1001/a","quote":"q"}`
	_, probs := Convert(Validation, true, vHandIn(t, "", vs, `{"source":"nowhere","quote":"q","problem":"p"}`), validationEvidence("claims"), Review{})
	want := []string{"verdict 2: source not found among the study's attachments or recorded sources",
		"flagged error 1: source not found among the study's attachments or recorded sources"}
	if !slices.Equal(probs, want) {
		t.Errorf("got %q\nwant %q", probs, want)
	}
}

func TestValidationReplies(t *testing.T) {
	prev := Review{Failed: map[string]string{"a stated claim": "r8", "doi:10.1001/a\nthe quote": "r9"}}
	h := parsed(t, Validation, `{`+validEmpty[:len(validEmpty)-len(`,"replies":[]`)]+`,"assessments":[],
		"verdicts":[`+verdictOK+`],"flagged_errors":[{"source":"10.1001/a","quote":"The  quote","problem":"p"}],"replies":[
		{"item":"A Stated  Claim","action":"withdrawn","reason":"unsupported"},
		{"item":"doi:10.1001/a the quote","action":"corrected","reason":"fixed"},
		{"item":"c","action":"corrected","reason":"fixed"}]}`)
	recs, probs := Convert(Validation, true, h, validationEvidence("claims"), prev)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if !slices.Equal(kinds(recs), []string{"verdict", "flagged_error", "withdrawn", "summary"}) || body(t, recs[2])["item"] != "r8" {
		t.Fatalf("kinds %v", kinds(recs))
	}
}

func TestValidationRetryAliases(t *testing.T) {
	prev := Review{Failed: map[string]string{"c": "r8", "retry-ref-example": "r8"}, Aliases: map[string]string{"retry-ref-example": "c"}}
	for _, action := range []string{"corrected", "withdrawn"} {
		h := parsed(t, Validation, `{`+validEmpty[:len(validEmpty)-len(`,"replies":[]`)]+`,"assessments":[],"verdicts":[`+verdictOK+`],"flagged_errors":[],"replies":[{"item":"retry-ref-example","action":"`+action+`","reason":"fixed"}]}`)
		_, probs := Convert(Validation, true, h, validationEvidence("claims"), prev)
		if len(probs) != 0 {
			t.Errorf("%s: %v", action, probs)
		}
	}
	for _, aliases := range []map[string]string{nil, {"retry-ref-example": ""}} {
		h := parsed(t, Validation, `{`+validEmpty[:len(validEmpty)-len(`,"replies":[]`)]+`,"assessments":[],"verdicts":[`+verdictOK+`],"flagged_errors":[],"replies":[{"item":"retry-ref-example","action":"withdrawn","reason":"fixed"}]}`)
		if _, probs := Convert(Validation, true, h, validationEvidence("claims"), Review{Failed: map[string]string{"c": "r8"}, Aliases: aliases}); len(probs) == 0 {
			t.Error("stale or ambiguous alias accepted")
		}
	}
	h := parsed(t, Validation, `{`+validEmpty[:len(validEmpty)-len(`,"replies":[]`)]+`,"assessments":[],"verdicts":[`+verdictOK+`],"flagged_errors":[],"replies":[{"item":"retry-ref-example","action":"corrected","reason":"fixed"}]}`)
	if _, probs := Convert(Validation, true, h, validationEvidence("claims"), Review{Failed: map[string]string{"c": "", "retry-ref-example": "r8"}, Aliases: map[string]string{"retry-ref-example": "c"}}); len(probs) == 0 {
		t.Error("alias targeting an ambiguous identity accepted")
	}
}

func TestOrdinaryRetryPrefixedIdentityIsNotAnAlias(t *testing.T) {
	h := parsed(t, Validation, `{`+validEmpty[:len(validEmpty)-len(`,"replies":[]`)]+`,"assessments":[],"verdicts":[{"claim":"retry-ref-ordinary","verdict":"supported","source":"data.csv","quote":"x,y"}],"flagged_errors":[],"replies":[{"item":"retry-ref-ordinary","action":"corrected","reason":"fixed"}]}`)
	_, probs := Convert(Validation, true, h, validationEvidence("claims"), Review{Failed: map[string]string{"retry-ref-ordinary": "r8"}})
	if len(probs) != 0 {
		t.Fatal(probs)
	}
}

func TestConvertObjector(t *testing.T) {
	h := parsed(t, Objector, `{"status":"done","answer":"Two problems.","reason":null,"objections":[
		{"target":"Rival B","type":"leakage","quote":"split after scaling","problem":"the test set was scaled with the train set"},
		{"target":"fit.txt","type":"overreach","quote":null,"problem":"one seed"}]}`)
	recs, probs := Convert(Objector, false, h, studyEvidence(), Review{})
	if len(probs) > 0 || !slices.Equal(kinds(recs), []string{"objection", "objection"}) {
		t.Fatalf("%v %v", kinds(recs), probs)
	}
	a := body(t, recs[0])
	if a["label"] != "Rival B" || a["type"] != "leakage" || a["quote"] != "split after scaling" || a["upheld"] != false || a["server"] != "" {
		t.Errorf("objection %v", a)
	}
	if b := body(t, recs[1]); b["quote"] != nil || b["label"] != "fit.txt" {
		t.Errorf("objection %v", b)
	}
	none := parsed(t, Objector, `{"status":"could_not_do","answer":"a","reason":"no files","objections":[]}`)
	if recs, probs := Convert(Objector, false, none, studyEvidence(), Review{}); len(recs) != 0 || len(probs) != 0 {
		t.Errorf("could_not_do: %v %v", recs, probs)
	}
}

func TestModeAAssessesEveryStandingHypothesis(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Hypotheses, "hypothesis", "r3", map[string]any{"text": "Slope rises with dose"}),
		rec(board.Hypotheses, "hypothesis", "r4", map[string]any{"text": "Intercept falls with age"}),
		rec(board.Hypotheses, "hypothesis", "r7", map[string]any{"text": "Sex changes the slope"}),
		rec(board.Hypotheses, "withdrawn", "r8", map[string]any{"item": "r7", "reason": "no data"}),
		rec(board.Experimentation, "value", "r5", map[string]any{"name": "slope", "value": "0.4"}),
		rec(board.Experimentation, "value", "r6", map[string]any{"name": "baseline", "value": "0.1"}),
	})
	e.Mode = "runs"
	_, probs := Convert(Validation, true, vHandIn(t, assessOK, "", ""), e, Review{})
	if want := []string{"hypothesis \"Intercept falls with age\" has no assessment"}; !slices.Equal(probs, want) {
		t.Fatalf("got %q, want %q", probs, want)
	}
}

func TestModeOf(t *testing.T) {
	summary := rec(board.Experimentation, "summary", "r9", map[string]any{"text": "ran"})
	summary.Role = "leader"
	hyp := rec(board.Hypotheses, "hypothesis", "r3", map[string]any{"text": "Slope rises with dose"})
	gone := rec(board.Hypotheses, "withdrawn", "r4", map[string]any{"item": "r3", "reason": "x"})
	for name, c := range map[string]struct {
		recs []db.Record
		want string
	}{
		"runs and hypotheses": {[]db.Record{hyp, summary}, "runs"},
		"runs, no hypotheses": {[]db.Record{summary}, "claims"},
		"runs, all withdrawn": {[]db.Record{hyp, gone, summary}, "claims"},
		"hypotheses, no runs": {[]db.Record{hyp}, "claims"},
	} {
		if got := ModeOf(c.recs); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestAVerdictMayCiteAnExperimentationOutput(t *testing.T) {
	recs := []db.Record{rec(board.Experimentation, "output", "o1", map[string]any{"name": "results.csv"}),
		rec(board.Validation, "output", "o2", map[string]any{"name": "notes.txt"})}
	e := NewEvidence(recs)
	e.Mode = "claims"
	vs := `{"claim":"c","verdict":"supported","source":"results.csv","quote":"q"},{"claim":"d","verdict":"supported","source":"notes.txt","quote":"q"}`
	_, probs := Convert(Validation, true, vHandIn(t, "", vs, ""), e, Review{})
	if want := []string{"verdict 2: source not found among the study's attachments or recorded sources"}; !slices.Equal(probs, want) {
		t.Fatalf("got %q, want %q", probs, want)
	}
}

func TestAValueFromASavedOutputCitesTheScriptRunsOfTheRunThatSavedIt(t *testing.T) {
	saver, other := uuid.New(), uuid.New()
	by := func(r db.Record, run uuid.UUID) db.Record { r.AgentRunID, r.Round = &run, 1; return r }
	ev := NewEvidence([]db.Record{
		by(rec(board.Experimentation, "run", "x3", map[string]any{"tool": "run_script", "path": "analysis.py", "exit_status": 0}), saver),
		by(rec(board.Experimentation, "run", "x4", map[string]any{"tool": "run_script", "path": "retry.py", "exit_status": 1}), saver),
		by(rec(board.Experimentation, "run", "x5", map[string]any{"tool": "run_script", "path": "other.py", "exit_status": 0}), other),
		by(rec(board.Experimentation, "output", "o2", map[string]any{"name": "results.csv"}), saver),
	})
	h := parsed(t, Experimentation, `{"status":"done","answer":"a","reason":null,"replies":[],"outputs":["results.csv"],
		"values":[{"name":"difference","value":"4.465","file":"results.csv"}]}`)
	recs, probs := Convert(Experimentation, true, h, ev, Review{})
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if v := body(t, recs[1]); !slices.Equal(v["cites"].([]any), []any{"run:x3"}) {
		t.Fatalf("value from a saved output %v", v)
	}
}

func TestAValueCanPinItsProducingScript(t *testing.T) {
	saver, other := uuid.New(), uuid.New()
	by := func(r db.Record, run uuid.UUID) db.Record { r.AgentRunID, r.Round = &run, 1; return r }
	ev := NewEvidence([]db.Record{
		by(rec(board.Experimentation, "run", "x1", map[string]any{"tool": "run_script", "path": "control.py", "exit_status": 0}), saver),
		by(rec(board.Experimentation, "run", "x2", map[string]any{"tool": "run_script", "path": "probe.py", "exit_status": 0}), saver),
		by(rec(board.Experimentation, "run", "x3", map[string]any{"tool": "run_script", "path": "other.py", "exit_status": 0}), other),
		by(rec(board.Experimentation, "output", "o2", map[string]any{"name": "outputs/probe.csv"}), saver),
	})
	h := parsed(t, Experimentation, `{"status":"done","answer":"The script printed a result.","reason":null,"replies":[],"outputs":["outputs/probe.csv"],
		"values":[{"name":"probe","value":"73","file":"outputs/probe.csv","run":"run:x2"}]}`)
	recs, probs := Convert(Experimentation, true, h, ev, Review{})
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if v := body(t, recs[1]); !slices.Equal(v["cites"].([]any), []any{"run:x2"}) {
		t.Fatalf("value lost its script attribution: %v", v)
	}
	wrong := "run:x3"
	h.Values[0].Run = &wrong
	if _, probs := Convert(Experimentation, true, h, ev, Review{}); len(probs) != 1 {
		t.Fatalf("an unrelated script was accepted: %v", probs)
	}
}

func TestAHypothesisMayCiteAnAttachedFile(t *testing.T) {
	ev := NewEvidence([]db.Record{
		rec(board.Hypotheses, "call", "c4", map[string]any{"server": "attachments", "tool": "read", "args": map[string]any{"name": "results.pdf"}, "answer": "Table 2"}),
	})
	h := parsed(t, Hypotheses, `{"status":"done","answer":"a","reason":null,"replies":[],"hypotheses":[{"text":"Statins lower LDL.",
		"prediction_shape":"direction","direction":"decrease","outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,
		"control":"placebo","no_control":null,"cites":["results.pdf"]}]}`)
	recs, probs := Convert(Hypotheses, true, h, ev, Review{})
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if b := body(t, recs[0]); !slices.Equal(b["cites"].([]any), []any{"call:c4"}) {
		t.Fatalf("hypothesis %v", b)
	}
}

func TestNestedEvidenceIsRejectedWithCorrectionWithoutExtraction(t *testing.T) {
	h := parsed(t, Grounding, `{"status":"done","answer":"{\"papers\":[{\"id\":\"doi:10.1001/a\",\"why\":\"meets conditions\"}]}","papers":[],"datasets":[],"no_qualifying":null,"replies":[]}`)
	recs, probs := Convert(Grounding, true, h, studyEvidence(), Review{})
	if len(recs) != 0 || len(h.Papers) != 0 || !strings.Contains(strings.Join(probs, "\n"), "siblings of answer") {
		t.Fatalf("records=%v papers=%v problems=%v", recs, h.Papers, probs)
	}
	// Correcting the position still requires recorded provenance.
	h.Answer = "A source supports this result."
	h.Papers = []Paper{{ID: "doi:10.1000/not-retrieved", Why: "supports result"}}
	recs, probs = Convert(Grounding, true, h, studyEvidence(), Review{})
	if len(recs) != 0 || !strings.Contains(strings.Join(probs, "\n"), "was not found through Lykeion's sources") {
		t.Fatalf("unrecorded evidence accepted: %v %v", recs, probs)
	}
	h.Papers[0].ID = "doi:10.1001/a"
	if _, probs = Convert(Grounding, true, h, studyEvidence(), Review{}); len(probs) != 0 {
		t.Fatal(probs)
	}
}

// Review fails a sent-back item left unanswered every time (seen live: a redo
// added a renamed value and left the failing one standing), so a redo must
// answer each one: hand it in again under one of its names, or withdraw it.
func TestARedoMustAnswerEverySentBackItem(t *testing.T) {
	prev := Review{Failed: map[string]string{"circularity": "r20", "0.076: circularity": "r20", "area": "r21"}}
	value := func(name string) Value { return Value{Name: name, Value: "0.076", File: "fit.py"} }
	for _, tc := range []struct {
		name    string
		h       HandIn
		missing []string
	}{
		{"renamed and unanswered", HandIn{Values: []Value{value("r20 0.076"), value("area")}}, []string{"circularity"}},
		{"nothing answered", HandIn{Values: []Value{value("other")}}, []string{"area", "circularity"}},
		{"handed in again", HandIn{Values: []Value{value("Circularity"), value("Area")}}, nil},
		{"under its other label", HandIn{Values: []Value{value("0.076: circularity"), value("area")}}, nil},
		{"withdrawn", HandIn{Values: []Value{value("area")}, Replies: []Reply{{Item: "circularity", Action: "withdrawn", Reason: "wrong unit"}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.h.Status, tc.h.Answer = "done", "redo"
			_, probs := Convert(Experimentation, true, tc.h, studyEvidence(), prev)
			var got []string
			for _, p := range probs {
				if item, ok := strings.CutPrefix(p, "the last review sent back "); ok {
					got = append(got, strings.SplitN(item, ":", 2)[0])
				} else {
					t.Errorf("unexpected problem %q", p)
				}
			}
			if !slices.Equal(got, tc.missing) {
				t.Errorf("unanswered %q, want %q", got, tc.missing)
			}
		})
	}
	// A first attempt has nothing sent back.
	if _, probs := Convert(Experimentation, true, HandIn{Status: "done", Answer: "a", Values: []Value{value("x")}}, studyEvidence(), Review{}); len(probs) != 0 {
		t.Errorf("first attempt: %q", probs)
	}
}

// Final review: an estimate is assessed by its measurement alone; a control
// or test would make the finding invalid with no way for Experimentation to fix it.
func TestEstimateAssessmentNamesOnlyItsMeasurement(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Hypotheses, "hypothesis", "r3", map[string]any{"text": "PR at 65", "prediction": map[string]any{"shape": "estimate"}}),
		rec(board.Experimentation, "value", "r5", map[string]any{"name": "prob", "value": "0.395"}),
		rec(board.Experimentation, "value", "r6", map[string]any{"name": "baseline", "value": "0.1"}),
	})
	e.Mode = "runs"
	for raw, bad := range map[string]bool{
		`{"hypothesis":"PR at 65","measurement":"prob","control":null,"observed":null,"test":null}`:                                                                     false,
		`{"hypothesis":"PR at 65","measurement":"prob","control":"baseline","observed":null,"test":null}`:                                                               true,
		`{"hypothesis":"PR at 65","measurement":"prob","control":null,"observed":null,"test":{"kind":"t","statistic":2.31,"df1":28,"df2":null,"p":0.03,"p_bound":"="}}`: true,
	} {
		h := parsed(t, Validation, `{`+validEmpty[:len(validEmpty)-len(`,"replies":[]`)]+`,"assessments":[`+raw+`],"verdicts":[],"flagged_errors":[],"replies":[]}`)
		_, probs := Convert(Validation, true, h, e, Review{})
		got := strings.Contains(strings.Join(probs, "\n"), "an estimate is assessed by its measurement alone")
		if got != bad {
			t.Errorf("%s: problems %q", raw, probs)
		}
	}
}
