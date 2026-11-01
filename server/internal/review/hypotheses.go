package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
)

type HypothesesInput struct {
	Question string
	Items    []db.Record          // this round's hypothesis records
	Cited    map[string]db.Record // every record a hypothesis cites, by key
	Carried  []Item               // items that were unclear on an earlier attempt
	Attempt  int
}

const hypothesesWeakest = "Which of these hypotheses is least supported by its cited evidence?"

// hypothesisQuestions are Jev's per-hypothesis questions. Only lookup is
// phrased so that yes is bad.
var hypothesisQuestions = map[string]string{
	"falsifiable": "Is this prediction specific enough that a result could prove it wrong?",
	"supported":   "Does the cited evidence support making this prediction?",
	"lookup":      "Does the cited evidence already contain the answer this study is testing?",
}

// predictionLine renders a prediction as one line.
func predictionLine(p Prediction) string {
	switch p.Shape {
	case "direction":
		return "Prediction: " + p.Direction
	case "outcomes":
		return fmt.Sprintf("Prediction: one of %s; chosen: %s", strings.Join(p.Outcomes, ", "), p.Chosen)
	case "range":
		if p.Low != nil && p.High != nil {
			return fmt.Sprintf("Prediction: between %g and %g (%s)", *p.Low, *p.High, p.Uncertainty)
		}
	}
	return "Prediction: " + p.Shape
}

// Hypotheses reviews a Hypotheses column: the server's cite check, Jev's
// three questions on each hypothesis, and Jev's pick of the weakest.
func Hypotheses(ctx context.Context, a Asker, in HypothesesInput) (Result, error) {
	r := Result{Column: board.Hypotheses, Attempt: in.Attempt}
	var asked []Item
	var askedText []string
	for _, rec := range in.Items {
		var b HypothesisBody
		if err := json.Unmarshal(rec.Body, &b); err != nil {
			return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
		}
		it := Item{Key: rec.Key, Kind: rec.Kind, Label: LabelOf(rec), Identity: IdentityOf(rec)}
		// The research question comes first, fenced: people write it.
		evidence := []string{"Research question: " + in.Question}
		cite := Check{ID: "cites_recorded", By: "server", Status: Pass}
		for _, k := range b.Cites {
			c, ok := in.Cited[k]
			if !ok {
				cite.Status, cite.Detail = Fail, "cites "+k+", which is not a current record"
				break
			}
			evidence = append(evidence, string(c.Body))
		}
		it.Checks = []Check{cite}
		if b.Control.None != "" {
			it.Checks = append(it.Checks, Check{ID: "control", By: "server", Detail: "no control; at most caveated in Validation", Status: Unclear})
		}
		if cite.Status == Pass {
			text := b.Text + "\n" + predictionLine(b.Prediction)
			ps, err := a.Noul(ctx, ItemState("Hypotheses", text, evidence, MaxState), hypothesisQuestions)
			if err != nil {
				return Result{}, err
			}
			for _, id := range []string{"falsifiable", "supported", "lookup"} {
				p, ok := ps[id]
				if !ok {
					return Result{}, fmt.Errorf("jev gave no answer for %s", id)
				}
				c := Check{ID: id, By: "jev", Question: hypothesisQuestions[id]}
				if id == "lookup" {
					p = 1 - p
				}
				c.Status = StatusOf(p)
				p = stored(p)
				c.P = &p
				it.Checks = append(it.Checks, c)
			}
			asked = append(asked, it)
			askedText = append(askedText, text)
		}
		it.Status = ItemStatus(it.Checks)
		r.Items = append(r.Items, it)
	}
	for _, c := range in.Carried {
		c.Carried = true
		r.Items = append(r.Items, c)
	}

	// The weakest-item options are neutral names; each hypothesis reaches
	// Jev only as fenced evidence.
	if len(asked) >= 2 {
		options := make(map[string]string, len(asked))
		keys := make(map[string]string, len(asked))
		var names []string
		for i, it := range asked {
			opt, name := fmt.Sprintf("hyp:%d", i+1), fmt.Sprintf("Hypothesis %d", i+1)
			options[opt], keys[opt] = name, it.Key
			names = append(names, fmt.Sprintf("Evidence %d is %s.", i+1, name))
		}
		ans, err := a.Choice(ctx, ItemState("Hypotheses", strings.Join(names, "\n"), askedText, MaxState), hypothesesWeakest, options)
		if err != nil {
			return Result{}, err
		}
		key, ok := keys[ans.Choice]
		if !ok {
			return Result{}, fmt.Errorf("jev chose %q, not one of the options", ans.Choice)
		}
		r.Weakest = &Weakest{Key: key, P: stored(ans.Probabilities[ans.Choice])}
	}
	return r, nil
}
