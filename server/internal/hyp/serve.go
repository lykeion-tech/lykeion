package hyp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	db "lykeion/server/pkg/db/generated"
)

// compareBudget bounds Jev's pairwise questions in one phase; what is left
// falls to the selector.
const compareBudget = 5 * time.Minute

const pairQuestion = "Which hypothesis is the better one to carry forward as a rival on this card? " +
	"Weigh first how sharply one measurement on the card's dataset could tell it apart from other explanations, " +
	"then whether it can be measured with that data and within the card's budget, and novelty last."

var pairOptions = map[string]string{
	"first":  "The first hypothesis is the better one to carry forward",
	"second": "The second hypothesis is the better one to carry forward",
}

// Serve runs on the server before one of the column's phases is queued, with
// no transaction open; the engine writes the batch it returns. It seeds the
// generators, removes duplicate candidates before the decomposer, and has Jev
// compare the survivors before the selector. It returns true when the gate
// should judge now instead: a role recorded a missing premise, or no
// candidate is left to work on.
func Serve(ctx context.Context, c db.Card, v *records.View, next []string, j *jev.Client) (records.Batch, bool, error) {
	if len(next) == 0 {
		return records.Batch{}, false, nil
	}
	if slices.Contains(Generators, next[0]) {
		b, err := seed(v)
		return b, false, err
	}
	if len(v.Kind("missing_premise")) > 0 || len(v.Kind("candidate")) == 0 {
		return records.Batch{}, true, nil
	}
	switch next[0] {
	case Decomposer:
		return dedup(v), false, nil
	case Selector:
		return compare(ctx, c, v, j), false, nil
	}
	return records.Batch{}, false, nil
}

func seed(v *records.View) (records.Batch, error) {
	gv, err := v.Column(board.Grounding)
	if err != nil {
		return records.Batch{}, err
	}
	var gaps []string
	if r, ok := gv.Latest("notes"); ok {
		gaps = Gaps(records.Body[struct{ Text string }](r).Text)
	}
	if r, ok := gv.Latest("question"); ok && len(gaps) == 0 {
		gaps = []string{strings.TrimSpace(records.Body[struct{ Text string }](r).Text)}
	}
	var sup, chal []string
	for _, r := range gv.Kind("evidence") {
		if records.Body[ground.Evidence](r).Stance == "challenges" {
			chal = append(chal, r.Key)
		} else {
			sup = append(sup, r.Key)
		}
	}
	b := records.Batch{Retire: []string{"seed"}}
	for i, s := range Pick(len(Generators), gaps, interleave(sup, chal)) {
		b.Put = append(b.Put, records.Put{Kind: "seed", Key: Generators[i], Body: s})
	}
	return b, nil
}

func dedup(v *records.View) records.Batch {
	var keys, texts []string
	for _, r := range v.Kind("candidate") {
		cd := records.Body[Candidate](r)
		parts := []string{cd.Statement}
		for _, s := range cd.Mechanism {
			parts = append(parts, s.Text)
		}
		keys, texts = append(keys, r.Key), append(texts, strings.Join(parts, " "))
	}
	surv, merged := Cluster(keys, texts, DedupThreshold)
	return records.Batch{Retire: []string{"dedup"}, Put: []records.Put{{Kind: "dedup",
		Body: Dedup{Survivors: surv, Merged: merged, Method: "tfidf-cosine", Threshold: DedupThreshold}}}}
}

func decompositions(v *records.View) map[string]Decomposition {
	out := map[string]Decomposition{}
	for _, r := range v.Kind("decomposition") {
		d := records.Body[Decomposition](r)
		out[d.Candidate] = d
	}
	return out
}

func novelties(v *records.View) map[string]Novelty {
	out := map[string]Novelty{}
	for _, r := range v.Kind("novelty") {
		n := records.Body[Novelty](r)
		out[n.Candidate] = n
	}
	return out
}

func candidates(v *records.View) map[string]Candidate {
	out := map[string]Candidate{}
	for _, r := range v.Kind("candidate") {
		out[r.Key] = records.Body[Candidate](r)
	}
	return out
}

func describe(c Candidate, d Decomposition, n Novelty) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)", c.Statement, c.Kind)
	for _, p := range d.Predictions {
		fmt.Fprintf(&b, "\n- predicts: %s", p.Text)
	}
	for _, t := range d.Tests {
		fmt.Fprintf(&b, "\n- test: %s", t.Procedure)
	}
	if n.Status != "" {
		fmt.Fprintf(&b, "\n- prior work: %s", n.Status)
	}
	return b.String()
}

// compare asks Jev about every surviving pair. Pairs it does not answer are
// left to the selector, which `lyk hyp rank` tells which ones remain.
func compare(ctx context.Context, c db.Card, v *records.View, j *jev.Client) records.Batch {
	b := records.Batch{Retire: []string{"pairwise"}}
	dd, ok := v.Latest("dedup")
	if j == nil || !ok {
		return b
	}
	sv := records.Body[Dedup](dd).Survivors
	cs, ds, ns := candidates(v), decompositions(v), novelties(v)
	jctx, cancel := context.WithTimeout(ctx, compareBudget)
	defer cancel()
	for i, a := range sv {
		for k, bb := range sv[i+1:] {
			first, second := a, bb
			if (i+k+1)%2 == 1 {
				first, second = bb, a
			}
			state := fmt.Sprintf("question: %s\n\nfirst: %s\n\nsecond: %s", c.Question,
				describe(cs[first], ds[first], ns[first]), describe(cs[second], ds[second], ns[second]))
			ans, err := j.Choice(jctx, state, pairQuestion, pairOptions)
			if err != nil {
				continue
			}
			pf, ps := ans.Probabilities["first"], ans.Probabilities["second"]
			if pf+ps <= 0 {
				continue
			}
			b.Put = append(b.Put, records.Put{Kind: "pairwise", Key: pairKey(first, second),
				Body: Judgment{A: first, B: second, PA: pf / (pf + ps), Judge: "jev", Model: j.Model}})
		}
	}
	return b
}
