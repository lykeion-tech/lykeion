package handin

import (
	"encoding/json"
	"slices"
	"testing"

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
			[]string{"hypothesis 2: cite 10.8888/made-up is neither a Grounding paper or dataset nor a source recorded in this study",
				"hypothesis 3: cite a Grounding paper or dataset of this study, or a source you looked up through Lykeion's tools"}},
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
		recs, probs := Convert(c.k, true, h, studyEvidence(), prev)
		if !slices.Equal(probs, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, probs, c.want)
		}
		if recs != nil {
			t.Errorf("%s: records %v", c.name, kinds(recs))
		}
	}
	// A review rule run on a converted body: a paper id of no known form.
	h := parsed(t, Grounding, `{"status":"done","answer":"a","reason":null,"replies":[],"papers":[{"id":"hits:","why":"w"}],"datasets":[],"no_qualifying":null}`)
	if _, probs := Convert(Grounding, true, h, studyEvidence(), prev); !slices.Equal(probs, []string{"paper 1: id must be arxiv:…, doi:10.… or pmid:…"}) {
		t.Errorf("item rule: %q", probs)
	}
}

func TestConvertReplies(t *testing.T) {
	prev := Review{Failed: map[string]string{"doi:10.1001/a": "r4", "a stated effect": "r6"}, Missed: []string{"doi:10.1001/b"}}
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
	recs, probs = Convert(Hypotheses, true, h, studyEvidence(), prev)
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
			[]string{"assessment 1: hypothesis Slope falls matches no hypothesis of this study"}},
		{"unknown measurement", "runs", `{"hypothesis":"Slope rises with dose","measurement":"intercept","control":"none","observed":null,"test":null}`, "", "",
			[]string{"assessment 1: measurement intercept matches no recorded value of this study", "assessment 1: control none matches no recorded value of this study"}},
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
	tst := a["Test"].(map[string]any)
	if a["Measurement"] != "slope" || a["Control"] != "baseline" || tst["StatDigits"] != 2.0 || tst["PDigits"] != 2.0 {
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
