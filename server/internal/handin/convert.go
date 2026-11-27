package handin

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
)

// Review records the preceding round's failed items, retry aliases, missing
// papers and the measure definitions that carry forward.
type Review struct {
	Failed          map[string]string
	Missed          []string
	Aliases         map[string]string
	CarriedMeasures []review.Measure
}

// NewRecord is one record a hand-in becomes, written on the run's behalf.
type NewRecord struct {
	Kind string
	Body any
}

type declinedBody struct {
	Item   string `json:"item"` // the missed paper's id
	Reason string `json:"reason"`
}

type summaryBody struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
}

type auditBody struct {
	Text     string   `json:"text"`
	Findings []string `json:"findings"`
}

var columnOf = map[Kind]string{Grounding: board.Grounding, Hypotheses: board.Hypotheses, Experimentation: board.Experimentation, Validation: board.Validation}

// identity is how a reply, a sent-back item and a declared item are compared:
// an id in its one spelling, text without case and with its spacing collapsed.
func identity(s string) string { return strings.Join(strings.Fields(NormID(s)), " ") }

// Convert turns a lead's or auditor's done hand-in, already parsed, into the
// records the review reads, or says what stops it, one plain line per
// problem. A member's hand-in and one that could not be done become no
// records: what counts is in the lead's.
func Convert(k Kind, lead bool, h HandIn, ev Evidence, prev Review) ([]NewRecord, []string) {
	if k == Autoresearch {
		return nil, []string{"not yet supported"}
	}
	if h.Status != "done" {
		return nil, nil
	}
	if k == Auditor {
		a := auditBody{Text: h.Answer, Findings: []string{}}
		for _, c := range h.Concerns {
			a.Findings = append(a.Findings, c.Item+": "+c.Concern)
		}
		return []NewRecord{{Kind: "audit", Body: a}}, nil
	}
	if k == Objector {
		// Filed, not yet judged: the review resolves each target and checks it.
		recs := []NewRecord{}
		for _, o := range h.Objections {
			recs = append(recs, NewRecord{Kind: "objection", Body: review.Objection{Label: o.Target, Type: o.Type, Quote: o.Quote, Problem: o.Problem, Checks: []review.Check{}}})
		}
		return recs, nil
	}
	if !lead {
		return nil, nil
	}
	c := converter{column: columnOf[k], ev: ev, declared: map[string]bool{}}
	switch k {
	case Grounding:
		c.grounding(h)
	case Hypotheses:
		c.hypotheses(h, prev.CarriedMeasures)
	case Experimentation:
		c.experimentation(h)
	case Validation:
		c.validation(h)
	}
	c.replies(h.Replies, prev)
	if len(c.probs) > 0 {
		var nested map[string]json.RawMessage
		if json.Unmarshal([]byte(h.Answer), &nested) == nil {
			for _, field := range []string{"papers", "datasets", "no_qualifying", "hypotheses", "outputs", "values", "assessments", "verdicts", "flagged_errors"} {
				if _, ok := nested[field]; ok {
					c.problem("answer: use researcher-facing prose; evidence fields belong at the top level as siblings of answer, using this hand-in's schema. Evidence inside the answer string is not submitted; correct the fields and submit again")
					break
				}
			}
		}
		return nil, c.probs
	}
	return append(c.recs, NewRecord{Kind: "summary", Body: summaryBody{Text: h.Answer, Cites: []string{}}}), nil
}

type converter struct {
	column   string
	ev       Evidence
	declared map[string]bool // identities of the items this hand-in declares
	recs     []NewRecord
	probs    []string
}

func (c *converter) problem(format string, a ...any) {
	c.probs = append(c.probs, fmt.Sprintf(format, a...))
}

// item adds an item record after the review's own rules for its kind; a
// broken rule is reported as label n's problem.
func (c *converter) item(label string, n int, kind string, body any) {
	b, err := json.Marshal(body)
	if err == nil {
		err = review.Validate(kind, c.column, b)
	}
	if err != nil {
		c.problem("%s %d: %s", label, n, strings.TrimPrefix(err.Error(), kind+": "))
		return
	}
	c.recs = append(c.recs, NewRecord{Kind: kind, Body: body})
}

// calls are the citable records among an id's sources: the calls that
// returned it. Found reports whether any recorded source returned it.
func (c *converter) calls(id string) (cites []string, found bool) {
	srcs := c.ev.Sources(id)
	cites = []string{}
	for _, s := range srcs {
		if strings.HasPrefix(s, "call:") {
			cites = append(cites, s)
		}
	}
	return cites, srcs != nil
}

func (c *converter) grounding(h HandIn) {
	if len(h.Papers) == 0 && len(h.Datasets) == 0 && h.NoQualifying == nil {
		c.problem("a done Grounding hand-in needs at least one paper, dataset or no_qualifying")
	}
	for i, p := range h.Papers {
		cites, ok := c.calls(p.ID)
		if !ok {
			c.problem("paper %d: %s was not found through Lykeion's sources", i+1, p.ID)
			continue
		}
		id := NormID(p.ID)
		c.declared[identity(id)] = true
		c.item("paper", i+1, review.KindPaper, review.PaperBody{ID: id, Text: p.Why, Cites: cites})
	}
	for i, d := range h.Datasets {
		cites, ok := c.calls(d.Accession)
		if !ok {
			c.problem("dataset %d: %s was not found through Lykeion's sources", i+1, d.Accession)
			continue
		}
		acc := strings.TrimSpace(d.Accession)
		c.declared[identity(acc)] = true
		c.item("dataset", i+1, review.KindDataset, review.DatasetBody{Accession: acc, Text: d.Why, Cites: cites})
	}
	if n := h.NoQualifying; n != nil {
		c.declared[identity(n.Reason)] = true
		c.item("no_qualifying", 1, review.KindNoQualifying, review.NoQualifyingBody{Text: n.Reason})
	}
}

// hypotheses cite the study's Grounding items by their record key, the
// study's attached files by the calls that read them, and other recorded
// sources by the calls that returned them.
func (c *converter) hypotheses(h HandIn, carried []review.Measure) {
	for i, x := range h.Hypotheses {
		if m := x.Measure; m != nil && slices.ContainsFunc(carried, func(old review.Measure) bool {
			return old.ID == m.ID && old != *m
		}) {
			c.problem("hypothesis %d: measure id %q already stands from the previous review with a different name or unit", i+1, m.ID)
		}
		cites, ok := []string{}, true
		for _, id := range x.Cites {
			keys, found := c.resolveCite(id)
			if !found {
				c.problem("hypothesis %d: cite %s is neither a Grounding paper or dataset, a file attached to the study, nor a source recorded in this study", i+1, id)
				ok = false
				continue
			}
			for _, k := range keys {
				if !slices.Contains(cites, k) {
					cites = append(cites, k)
				}
			}
		}
		var reported *review.ReportedValue
		if r := x.Reported; r != nil {
			keys, found := c.resolveCite(r.Cite)
			if !found || len(keys) == 0 {
				c.problem("hypothesis %d: reported cite %s is neither a Grounding paper or dataset, a file attached to the study, nor a source recorded in this study", i+1, r.Cite)
				ok = false
			} else {
				reported = &review.ReportedValue{Value: r.Value, Source: NormID(r.Cite), Key: keys[0]}
				// Cited too, so review checks the source is current and Jev reads it.
				if !slices.Contains(cites, keys[0]) {
					cites = append(cites, keys[0])
				}
			}
		}
		if !ok {
			continue
		}
		c.declared[identity(x.Text)] = true
		if len(cites) == 0 {
			// Hosted search and fetch hits are no record a hypothesis can cite.
			c.problem("hypothesis %d: cite a Grounding paper or dataset of this study, a file attached to it, or a source you looked up through Lykeion's tools", i+1)
			continue
		}
		b := x.Body(cites)
		b.Reported = reported
		c.item("hypothesis", i+1, review.KindHypothesis, b)
	}
}

// resolveCite is the records a hypothesis cite stands for: a Grounding paper
// or dataset, or the calls that read an attachment or looked up a source.
func (c *converter) resolveCite(id string) ([]string, bool) {
	if key, g := c.ev.Grounding(id); g {
		return []string{key}, true
	}
	calls, found := c.calls(id)
	if read := c.ev.AttachmentCalls(id); len(read) > 0 {
		calls, found = read, true
	}
	return calls, found
}

func (c *converter) experimentation(h HandIn) {
	if len(h.Values) == 0 && len(h.Outputs) == 0 {
		c.problem("a done Experimentation hand-in needs at least one value or output")
	}
	for i, o := range h.Outputs {
		if !c.ev.Saved(o) && !slices.ContainsFunc(h.Values, func(v Value) bool { return v.hasBinding() && v.File == o }) {
			c.problem("output %d: %s was not saved in this study", i+1, o)
		}
	}
	if len(h.Outputs) > 0 {
		c.declared[review.KindOutputs] = true
		c.item("outputs", 1, review.KindOutputs, review.OutputsBody{Files: h.Outputs})
	}
	for i, v := range h.Values {
		if v.hasBinding() {
			// Keep the claim as supplied. The review checks the exact run,
			// file and field together and can send back a failed binding.
			cites := []string{}
			if run := strings.TrimSpace(deref(v.Run)); run != "" {
				cites = append(cites, run)
			}
			c.recs = append(c.recs, NewRecord{Kind: "value", Body: review.ValueBody{
				Name: v.Name, Value: v.Value, File: v.File, Cites: cites, Field: v.Field, Measure: v.Measure,
			}})
			continue
		}
		file := v.File
		runs := c.ev.Scripts(file)
		scriptRun := false
		if path, ok := c.ev.ScriptPath(file); ok && !c.ev.Saved(file) && runs == nil {
			runs = []string{file}
			file = path
			scriptRun = true
		}
		if run := strings.TrimSpace(deref(v.Run)); run != "" {
			if !slices.Contains(runs, run) {
				c.problem("value %d: %s does not identify a recorded script run for %s", i+1, run, v.File)
				continue
			}
			runs = []string{run}
		}
		switch {
		case v.File == "":
			c.problem("value %d: name the saved output or script run that shows the value", i+1)
		case !c.ev.Saved(v.File) && runs == nil:
			c.problem("value %d: file %s names neither a saved output nor a script run recorded in this study", i+1, v.File)
		default:
			if runs == nil {
				runs = []string{}
			}
			c.recs = append(c.recs, NewRecord{Kind: "value", Body: review.ValueBody{Name: v.Name, Value: v.Value, File: file, Cites: runs, ScriptRun: scriptRun}})
		}
	}
}

// validation takes assessments of the study's hypotheses against its recorded
// values when Experimentation handed in runs, and otherwise verdicts and
// flagged errors on the study's claims, each resting on a known source.
func (c *converter) validation(h HandIn) {
	if c.ev.Mode == "runs" {
		if len(h.Verdicts) > 0 || len(h.FlaggedErrs) > 0 {
			c.problem("this study has recorded runs: hand in assessments, not verdicts")
		}
		if len(h.Assessments) == 0 {
			c.problem("a done Validation hand-in needs at least one assessment")
		}
	} else {
		if len(h.Assessments) > 0 {
			c.problem("this study has no recorded runs: hand in verdicts, not assessments")
		}
		if len(h.Verdicts) == 0 && len(h.FlaggedErrs) == 0 {
			c.problem("a done Validation hand-in needs at least one verdict or flagged error")
		}
	}
	if len(c.probs) > 0 {
		return
	}
	for i, a := range h.Assessments {
		ok := true
		if !c.ev.Hypothesis(a.Hypothesis) {
			c.problem("assessment %d: hypothesis %s matches no hypothesis of this study", i+1, a.Hypothesis)
			ok = false
		}
		if c.ev.Estimate(a.Hypothesis) && (a.Control != nil || a.Observed != nil || a.Test != nil) {
			// Review would make it invalid, and Experimentation cannot fix an assessment.
			c.problem("assessment %d: an estimate is assessed by its measurement alone: leave control, observed and test null", i+1)
			ok = false
		}
		for _, v := range []struct {
			what string
			name *string
		}{{"measurement", &a.Measurement}, {"control", a.Control}, {"observed", a.Observed}} {
			if v.name != nil && !c.ev.Value(*v.name) {
				c.problem("assessment %d: %s %s matches no recorded value of this study", i+1, v.what, *v.name)
				ok = false
			}
		}
		if ok {
			c.declared[identity(a.Hypothesis)] = true
			c.item("assessment", i+1, review.KindAssessment, a.Assessment())
		}
	}
	for i, v := range h.Verdicts {
		if src, ok := c.source(v.Source); ok {
			c.declared[identity(v.Claim)] = true
			c.item("verdict", i+1, review.KindVerdict, review.VerdictBody{Claim: v.Claim, Verdict: v.Verdict, Source: src, Quote: v.Quote})
		} else {
			c.problem("verdict %d: source not found among the study's attachments or recorded sources", i+1)
		}
	}
	for i, f := range h.FlaggedErrs {
		if src, ok := c.source(f.Source); ok {
			// The identity review.IdentityOf gives it; a reply's spacing
			// collapses the line break, as identity does here.
			c.declared[identity(src+"\n"+f.Quote)] = true
			c.item("flagged error", i+1, review.KindFlaggedError, review.FlaggedErrorBody{Source: src, Quote: f.Quote, Problem: f.Problem})
		} else {
			c.problem("flagged error %d: source not found among the study's attachments or recorded sources", i+1)
		}
	}
	// Every hypothesis that stands is assessed: a missing one is the lead's
	// to add, not Experimentation's to redo.
	if c.ev.Mode == "runs" {
		for _, r := range c.ev.Standing() {
			text := strings.TrimSpace(records.Body[review.HypothesisBody](r).Text)
			if !slices.ContainsFunc(h.Assessments, func(a Assessment) bool { return strings.TrimSpace(a.Hypothesis) == text }) {
				c.problem("hypothesis %q has no assessment", review.LabelOf(r))
			}
		}
	}
}

// source is s as a verdict cites it: an attached file's or a file
// Experimentation saved's name as given, or a source id a call of the study
// returned, in its normalised spelling.
func (c *converter) source(s string) (string, bool) {
	if c.ev.Attached(s) || c.ev.ExperimentationOutput(s) {
		return s, true
	}
	if cites, _ := c.calls(s); len(cites) > 0 {
		return NormID(s), true
	}
	return "", false
}

// replies answer the items the last review sent back: a withdrawn item is
// named by its record's key, a declined missed paper by its id, and a
// corrected item must be declared again in this hand-in.
func (c *converter) replies(rs []Reply, prev Review) {
	failed := map[string]string{}
	for id, key := range prev.Failed {
		id = identity(id)
		if old, exists := failed[id]; exists && old != key {
			failed[id] = ""
		} else if !exists {
			failed[id] = key
		}
	}
	missed := map[string]bool{}
	for _, id := range prev.Missed {
		missed[identity(id)] = true
	}
	answered := map[string]bool{}
	replied := map[string]bool{} // record keys a reply names
	defer func() {
		// Review fails a sent-back item left unanswered every time, so a redo
		// that leaves one is refused here, while the agent can still answer it.
		// An item can have several names; any of them answers it.
		ids := slices.Sorted(maps.Keys(failed))
		for _, id := range ids {
			if c.declared[id] || c.namesValue(failed, id) {
				replied[failed[id]] = true
			}
		}
		// One problem per item, under its shortest name: its plain name, not
		// a display label such as "0.42: slope".
		slices.SortStableFunc(ids, func(a, b string) int { return len(a) - len(b) })
		for _, id := range ids {
			if key := failed[id]; key != "" && !replied[key] {
				replied[key] = true
				c.problem("the last review sent back %s: hand it in again corrected under the same name, or withdraw it in replies", id)
			}
		}
	}()
	for i, r := range rs {
		id := identity(r.Item)
		if target, isAlias := prev.Aliases[id]; isAlias {
			original := identity(target)
			if original == "" || failed[id] == "" || failed[id] != failed[original] {
				c.problem("reply %d: retry reference is stale or ambiguous", i+1)
				continue
			}
			id = original
		}
		if key := failed[id]; key != "" {
			replied[key] = true
		}
		if key := failed[id]; c.column == board.Experimentation && key != "" && (r.Action == "corrected" || r.Action == "withdrawn") {
			if answered[key] {
				c.problem("reply %d: the same item was already answered", i+1)
				continue
			}
			answered[key] = true
		}
		switch r.Action {
		case "corrected":
			// Values are named in a hand-in; a review may also refer to the
			// same value by its displayed label or earlier identity.
			if c.namesValue(failed, id) {
				continue
			}
			if !c.declared[id] {
				c.problem("reply %d: corrected, but no item %s is in this hand-in", i+1, r.Item)
			}
		case "declined":
			if !missed[id] {
				c.problem("reply %d: only a missed paper the review sent back can be declined", i+1)
				continue
			}
			c.recs = append(c.recs, NewRecord{Kind: "declined", Body: declinedBody{Item: NormID(r.Item), Reason: r.Reason}})
		case "withdrawn":
			key, ok := failed[id]
			if !ok || key == "" {
				c.problem("reply %d: %s was not sent back by the last review, so it cannot be withdrawn", i+1, r.Item)
				continue
			}
			c.item("reply", i+1, review.KindWithdrawn, review.WithdrawnBody{Item: key, Reason: r.Reason})
		}
	}
}

// namesValue is whether this Experimentation hand-in declares a value the
// review knew as the sent-back item id, by its name or another label.
func (c *converter) namesValue(failed map[string]string, id string) bool {
	return c.column == board.Experimentation && failed[id] != "" && slices.ContainsFunc(c.recs, func(rec NewRecord) bool {
		v, ok := rec.Body.(review.ValueBody)
		return ok && failed[identity(v.Name)] == failed[id]
	})
}
