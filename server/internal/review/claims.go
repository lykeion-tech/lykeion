package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
)

type ClaimsInput struct {
	Items   []db.Record       // this round's verdict and flagged_error records
	Sources map[string]string // each source id (or attachment name) to its text
	Carried []Item            // items that were unclear on an earlier attempt
	Attempt int32
}

const claimsWeakest = "Which of these claims is least supported by its quoted passage?"

const verdictInstructions = "Reading only the quoted passage, does it support the claim, refute it, or give not enough information to tell?"

var verdictOptions = map[string]string{
	"supported":       "The passage supports the claim.",
	"refuted":         "The passage contradicts the claim.",
	"not_enough_info": "The passage does not say enough to tell.",
}

// flaggedQuestions are Jev's questions on a flagged error.
var flaggedQuestions = map[string]string{
	"wrong":    "Is the passage wrong in the way stated?",
	"specific": "Is the criticism specific to this passage rather than generic?",
}

// verdictStatus compares Jev's blind pick with the Team's verdict. p is
// Jev's probability for its own pick.
//
//	same pick, p >= 0.35        pass
//	same pick, p <  0.35        unclear (Jev barely leans)
//	different pick, p >= 0.70   fail
//	different pick, p <  0.70   unclear
func verdictStatus(same bool, p float64) string {
	switch {
	case same && p >= 0.35:
		return Pass
	case !same && p >= 0.70:
		return Fail
	}
	return Unclear
}

// Claims reviews Validation's claim checking: each verdict's and flagged
// error's quote must stand verbatim in its source (a server check, so an
// unknown source fails too); then Jev judges the quote without seeing the
// Team's verdict, and the two are compared.
func Claims(ctx context.Context, a Asker, in ClaimsInput) (Result, error) {
	r := Result{Column: board.Validation, Attempt: int(in.Attempt)}
	var asked []Item
	var askedText []string
	for _, rec := range in.Items {
		it := Item{Key: rec.Key, Kind: rec.Kind, Label: LabelOf(rec), Identity: IdentityOf(rec)}
		var source, quote string
		var vb VerdictBody
		var fb FlaggedErrorBody
		switch rec.Kind {
		case KindVerdict:
			if err := json.Unmarshal(rec.Body, &vb); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			source, quote = vb.Source, vb.Quote
		case KindFlaggedError:
			if err := json.Unmarshal(rec.Body, &fb); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			source, quote = fb.Source, fb.Quote
		default:
			continue
		}
		text, known := in.Sources[source]
		found := Check{ID: "quote_in_source", By: "server", Status: Pass}
		switch {
		case !known:
			found.Status, found.Detail = Fail, "the source "+source+" is not one of this study's sources"
		case !quoteIn(text, quote):
			found.Status, found.Detail = Fail, "the quote is not verbatim in "+source
		}
		it.Checks = []Check{found}
		if found.Status == Pass {
			var itemText string
			if rec.Kind == KindVerdict {
				itemText = "Claim: " + vb.Claim
				c, err := judge(ctx, a, itemText, quote, vb.Verdict)
				if err != nil {
					return Result{}, err
				}
				it.Checks = append(it.Checks, c)
			} else {
				itemText = "Passage: " + quote + "\nCriticism: " + fb.Problem
				ps, err := a.Noul(ctx, ItemState("Validation", itemText, []string{text}, MaxState), flaggedQuestions)
				if err != nil {
					return Result{}, err
				}
				for _, id := range []string{"wrong", "specific"} {
					p, ok := ps[id]
					if !ok {
						return Result{}, fmt.Errorf("jev gave no answer for %s", id)
					}
					c := Check{ID: id, By: "jev", Question: flaggedQuestions[id], Status: StatusOf(p)}
					p = stored(p)
					c.P = &p
					it.Checks = append(it.Checks, c)
				}
			}
			asked = append(asked, it)
			askedText = append(askedText, itemText+"\nQuote: "+quote)
		}
		it.Status = ItemStatus(it.Checks)
		r.Items = append(r.Items, it)
	}
	for _, c := range in.Carried {
		c.Carried = true
		r.Items = append(r.Items, c)
	}

	// The weakest-item options are neutral names; each item reaches Jev only
	// as fenced evidence.
	if len(asked) >= 2 {
		options := make(map[string]string, len(asked))
		keys := make(map[string]string, len(asked))
		var names []string
		for i, it := range asked {
			opt, name := fmt.Sprintf("claim:%d", i+1), fmt.Sprintf("Claim %d", i+1)
			options[opt], keys[opt] = name, it.Key
			names = append(names, fmt.Sprintf("Evidence %d is %s.", i+1, name))
		}
		ans, err := a.Choice(ctx, ItemState("Validation", strings.Join(names, "\n"), askedText, MaxState), claimsWeakest, options)
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

// quoteIn says whether quote is in text word for word. Runs of whitespace
// count as one space on both sides, so line breaks from a PDF do not matter;
// an empty quote is in nothing.
func quoteIn(text, quote string) bool {
	q := strings.Join(strings.Fields(quote), " ")
	return q != "" && strings.Contains(strings.Join(strings.Fields(text), " "), q)
}

// judge asks Jev for its verdict on a claim from the quote alone, and
// compares it with the Team's.
func judge(ctx context.Context, a Asker, claim, quote, verdict string) (Check, error) {
	ans, err := a.Choice(ctx, ItemState("Validation", claim, []string{quote}, MaxState), verdictInstructions, verdictOptions)
	if err != nil {
		return Check{}, err
	}
	if _, ok := verdictOptions[ans.Choice]; !ok {
		return Check{}, fmt.Errorf("jev chose %q, not one of the options", ans.Choice)
	}
	p := ans.Probabilities[ans.Choice]
	c := Check{ID: "verdict", By: "jev", Question: verdictInstructions, Status: verdictStatus(ans.Choice == verdict, p)}
	c.Detail = fmt.Sprintf("Jev read the quote as %s (%.0f%%); the Team said %s", ans.Choice, p*100, verdict)
	p = stored(p)
	c.P = &p
	return c, nil
}
