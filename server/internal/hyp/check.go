package hyp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/predicate"
	"lykeion/server/internal/records"
)

var relevance = map[string]string{
	"relevant":   "A result of this test bears directly on whether the targeted assumption or prediction holds",
	"irrelevant": "A result of this test would not bear on the targeted assumption or prediction",
}

var priorWork = map[string]string{
	"states_it": "The prior work already states this hypothesis",
	"does_not":  "The prior work does not state this hypothesis, though it may touch on it",
}

type reason struct {
	Reason string `json:"reason"`
}

// Check runs the code checks and, if none fails, the Jev checks. It returns a
// decided gate when no reviewer is needed; otherwise Verdict is empty.
func Check(ctx context.Context, v *records.View, j *jev.Client) (*board.Gate, error) {
	g := &board.Gate{Column: board.Hypotheses, InputDigest: v.Digest(ground.ReviewKinds...)}
	if r, ok := v.Latest("cannot_proceed"); ok {
		g.Verdict, g.Reason = board.CannotProceed, "no viable hypothesis: "+records.Body[reason](r).Reason
		return g, nil
	}
	ms, err := activeMissingPremises(v)
	if err != nil {
		return nil, err
	}
	if len(ms) > 0 {
		var gaps []string
		for _, r := range ms {
			gaps = append(gaps, records.Body[MissingPremise](r).Handoff())
		}
		ch := ground.CodeCheck("premise.missing", records.Body[MissingPremise](ms[0]).Step, false, "missing premise: "+strings.Join(gaps, "; "))
		ch.To = board.Grounding
		g.Checks = []board.Check{ch}
		board.Decide(g)
		return g, nil
	}
	cands := v.Kind("candidate")
	if len(cands) == 0 {
		var why []string
		for _, r := range v.Kind("nothing_found") {
			if slices.Contains(Generators, r.Role) {
				why = append(why, records.Body[reason](r).Reason)
			}
		}
		if len(why) > 0 {
			g.Verdict, g.Reason = board.CannotProceed, "no viable hypothesis: "+strings.Join(why, "; ")
			return g, nil
		}
	}
	gv, err := v.Column(board.Grounding)
	if err != nil {
		return nil, err
	}
	ev := map[string]bool{}
	for _, r := range gv.Kind("evidence") {
		ev[r.Key] = true
	}
	g.Checks = append(g.Checks, ground.CodeCheck("candidates.present", "", len(cands) > 0, "no candidate was recorded"))
	for _, r := range cands {
		c := records.Body[Candidate](r)
		g.Checks = append(g.Checks, ground.CodeCheck("claims."+r.Key, r.Role, cites(ev, c.Mechanism, c.Assumptions),
			"a mechanism step or assumption cites no live Grounding claim"))
	}
	dec, nov := decompositions(v), novelties(v)
	for _, h := range slices.Sorted(maps.Keys(dec)) {
		ok := true
		for _, a := range dec[h].Assumptions {
			ok = ok && len(a.Claims) > 0
			for _, e := range a.Claims {
				ok = ok && ev[e]
			}
		}
		g.Checks = append(g.Checks, ground.CodeCheck("claims."+h+".decomposition", Decomposer, ok, "an assumption cites no live Grounding claim"))
	}
	mr, hasMatrix := v.Latest("matrix")
	g.Checks = append(g.Checks, ground.CodeCheck("matrix.present", Selector, hasMatrix, "no matrix was recorded"))
	var m Matrix
	if hasMatrix {
		m = records.Body[Matrix](mr)
		g.Checks = append(g.Checks, matrixChecks(m, gv, v)...)
		in, err := rivalInputs(v, m, dec, nov)
		if err != nil {
			return nil, err
		}
		g.Checks = append(g.Checks, in...)
	}
	for _, ch := range g.Checks {
		if ch.Result == board.Fail {
			board.Decide(g)
			return g, nil
		}
	}
	g.Checks = append(g.Checks, jevChecks(ctx, gv, j, m, candidates(v), dec, nov)...)
	return g, nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func cites(ev map[string]bool, groups ...[]Cited) bool {
	for _, cs := range groups {
		for _, c := range cs {
			if len(c.Claims) == 0 {
				return false
			}
			for _, e := range c.Claims {
				if !ev[e] {
					return false
				}
			}
		}
	}
	return true
}

func matrixChecks(m Matrix, gv, v *records.View) []board.Check {
	var out []board.Check
	add := func(id string, ok bool, note string) { out = append(out, ground.CodeCheck(id, Selector, ok, note)) }
	err := shape(m)
	add("matrix.schema", err == nil, errText(err))
	if err != nil {
		return out
	}
	var sv []string
	if dd, ok := v.Latest("dedup"); ok {
		sv = records.Body[Dedup](dd).Survivors
	}
	nulls, carried := 0, map[string]bool{}
	var strays []string
	for _, r := range m.Rivals {
		if r.Kind == "null" {
			nulls++
			continue
		}
		carried[r.Candidate] = true
		if !slices.Contains(sv, r.Candidate) {
			strays = append(strays, r.Candidate)
		}
	}
	add("rivals.count", len(m.Rivals) >= 2 && len(m.Rivals) <= 4, fmt.Sprintf("%d rivals; 2 to 4 are needed", len(m.Rivals)))
	add("rivals.null", nulls > 0, "no null rival")
	add("rivals.survivors", len(strays) == 0, "rivals carry candidates that did not survive dedup: "+strings.Join(strays, ", "))
	rk, rerr := rank(sv, judgments(append(v.Kind("pairwise"), v.Kind("comparison")...)))
	add("ranking.complete", rerr == nil, errText(rerr))
	if rerr == nil {
		stored, ok := v.Latest("ranking")
		add("ranking.current", ok && slices.Equal(records.Body[Ranking](stored).Order, rk.Order),
			"the recorded ranking is missing or older than the judgments: run `lyk hyp rank`")
		add("select.top", carried[rk.Order[0]], "the top-ranked candidate "+rk.Order[0]+" is not a rival")
		for _, jd := range rk.Judgments {
			if jd.Judge != "jev" {
				out = append(out, board.Check{ID: "ranking.judges", Judge: board.Reviewer, Owner: Selector, Result: board.Attention,
					Note: "the selector judged pairs Jev did not; the reviewer confirms those judgments are fair"})
				break
			}
		}
	}
	_, opened := gv.ByKey("dataset", m.Measurement.Dataset)
	add("measurement.dataset", opened, m.Measurement.Dataset+" is not a dataset Grounding opened")
	unit := groundingUnit(gv.Kind("gate"), m.Measurement.Dataset)
	add("measurement.unit", unit != "" && unit == m.Measurement.Unit, "the measurement's unit of analysis is not the one Grounding settled")
	return append(out, predicateChecks(m)...)
}

// overlap describes an outcome on which every predicate holds, or returns ""
// when none exists. A search too large to run counts as overlap.
func overlap(ord []string, ps ...*predicate.Pred) string {
	pt, ok, err := predicate.Joint(ord, ps...)
	if err != nil {
		return err.Error()
	}
	if !ok {
		return ""
	}
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(pt)) {
		parts = append(parts, fmt.Sprintf("%s=%g", k, pt[k]))
	}
	return "both hold at " + strings.Join(parts, " ")
}

// predicateChecks judges what the predicates mean: each is a possible
// outcome; a rival's prediction, falsifier and indeterminate region exclude
// one another; the null holds only inside its equivalence region and every
// other prediction outside it; and no outcome satisfies two rivals'
// predictions, so the one measurement can tell every pair apart.
func predicateChecks(m Matrix) []board.Check {
	var out []board.Check
	add := func(id, problem, note string) {
		out = append(out, ground.CodeCheck(id, Selector, problem == "", strings.TrimSpace(note+" "+problem)))
	}
	ord := m.Measurement.Ordered
	preds := map[string]*predicate.Pred{}
	for _, r := range m.Rivals {
		p, _ := predicate.Parse(r.Prediction) // shape has parsed all three
		f, _ := predicate.Parse(r.Falsifier)
		ind, _ := predicate.Parse(r.Indeterminate)
		preds[r.ID] = p
		impossible := ""
		for _, x := range []struct {
			name string
			p    *predicate.Pred
		}{{"prediction", p}, {"falsifier", f}, {"indeterminate region", ind}} {
			if _, ok, err := predicate.Joint(ord, x.p); err != nil || !ok {
				impossible = "the " + x.name + " can never hold"
			}
		}
		add("stated."+r.ID, impossible, "")
		clash := overlap(ord, p, f)
		if clash == "" {
			clash = overlap(ord, p, ind)
		}
		if clash == "" {
			clash = overlap(ord, f, ind)
		}
		add("consistent."+r.ID, clash, "the prediction, falsifier and indeterminate region must exclude one another:")
		in := predicate.Within(r.Equivalence.Key, r.Equivalence.Low, r.Equivalence.High)
		if r.Kind == "null" {
			add("equivalence."+r.ID, overlap(ord, p, predicate.Not(in)), "the null must hold only inside its equivalence region, so it can be supported:")
		} else {
			add("equivalence."+r.ID, overlap(ord, p, in), "the prediction must exclude the equivalence region, where the effect is practically absent:")
		}
	}
	for i, a := range m.Rivals {
		for _, b := range m.Rivals[i+1:] {
			add("disagree."+a.ID+"."+b.ID, overlap(ord, preds[a.ID], preds[b.ID]),
				"one outcome satisfies both predictions, so the measurement cannot tell these rivals apart:")
		}
	}
	return out
}

// rivalInputs checks what each carried candidate needs: a decomposition with
// a falsification test, and a novelty search that ran, quoting what it says
// it found.
func rivalInputs(v *records.View, m Matrix, dec map[string]Decomposition, nov map[string]Novelty) ([]board.Check, error) {
	logged := map[string]bool{}
	for _, r := range v.Kind("search") {
		if r.Role == NoveltyScout {
			logged[records.Body[searchLog](r).Query] = true
		}
	}
	var out []board.Check
	for _, r := range m.Rivals {
		if r.Kind == "null" {
			continue
		}
		d, ok := dec[r.Candidate]
		out = append(out, ground.CodeCheck("decomposition."+r.ID, Decomposer, ok && len(d.Tests) > 0, r.Candidate+" has no decomposition with a falsification test"))
		n, ok := nov[r.Candidate]
		ran := ok && len(n.Queries) > 0
		for _, q := range n.Queries {
			ran = ran && logged[q]
		}
		out = append(out, ground.CodeCheck("novelty."+r.ID, NoveltyScout, ran, "no logged novelty search for "+r.Candidate))
		if !ok || n.Closest == nil {
			continue
		}
		f, fetched := v.ByKey("fetch", n.Closest.SourceID)
		txt, err := v.Text(f)
		if err != nil {
			return nil, err
		}
		q := ground.CodeCheck("novelty."+r.ID+".quote", NoveltyScout,
			fetched && strings.Contains(ground.Normalize(txt), ground.Normalize(n.Closest.Quote)), "the closest-work quote is not in the fetched text")
		q.Evidence = []string{"novelty#" + r.Candidate, f.BlobKey}
		out = append(out, q)
	}
	return out, nil
}

func want(option string) func(jev.Answer) (board.Result, float64) {
	return func(a jev.Answer) (board.Result, float64) {
		return board.DefaultBands.Result(a.Probabilities[option]), a.Probabilities[option]
	}
}

func jevChecks(ctx context.Context, gv *records.View, j *jev.Client, m Matrix, cs map[string]Candidate, dec map[string]Decomposition, nov map[string]Novelty) []board.Check {
	var out []board.Check
	for _, r := range m.Rivals {
		if r.Kind == "null" {
			continue
		}
		c, d := cs[r.Candidate], dec[r.Candidate]
		targets := map[string]string{}
		for _, a := range d.Assumptions {
			targets[a.ID] = a.Text
			for _, e := range a.Claims {
				er, _ := gv.ByKey("evidence", e)
				quote := records.Body[ground.Evidence](er).Quote
				ch := board.Check{ID: "assumption." + r.Candidate + "." + a.ID + "." + e, Owner: Decomposer,
					Evidence: []string{"decomposition#" + r.Candidate, "evidence#" + e},
					Question: "How does the quoted evidence relate to the assumption? (expected: supports)"}
				out = append(out, ground.Ask(ctx, j, ch, "claim: "+a.Text+"\n\npassage: "+quote, ground.Relation, want("supports")))
			}
		}
		for _, p := range d.Predictions {
			targets[p.ID] = p.Text
		}
		for _, t := range d.Tests {
			ch := board.Check{ID: "test." + r.Candidate + "." + t.ID, Owner: Decomposer, Evidence: []string{"decomposition#" + r.Candidate},
				Question: "Is this falsification test relevant to the hypothesis?"}
			state := fmt.Sprintf("hypothesis: %s\n\ntargeted: %s\n\ntest null: %s\ntest alternative: %s\nprocedure: %s",
				c.Statement, targets[t.Targets], t.Null, t.Alternative, t.Procedure)
			out = append(out, ground.Ask(ctx, j, ch, state, relevance, want("relevant")))
		}
		if n, ok := nov[r.Candidate]; ok && n.Closest != nil {
			expect := "does_not"
			if n.Status == "already_stated" {
				expect = "states_it"
			}
			ch := board.Check{ID: "prior." + r.ID, Owner: NoveltyScout, Evidence: []string{"novelty#" + r.Candidate, "fetch#" + n.Closest.SourceID},
				Question: "Does the closest prior work already state this hypothesis? (recorded: " + n.Status + ")"}
			out = append(out, ground.Ask(ctx, j, ch, "hypothesis: "+c.Statement+"\n\nclosest prior work: "+n.Closest.Quote, priorWork, want(expect)))
		}
	}
	return out
}
