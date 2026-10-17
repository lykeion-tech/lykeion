// Package gate asks Jev the meaning-level questions a stage gate needs,
// retries a failing Jev within the gate's budget, hands what Jev cannot answer
// to the reviewer, and records every answer as a choice.
package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
)

// Question is one Jev choice among candidates the code extracted.
type Question struct {
	ID, State, Instructions string
	Options                 map[string]string // code-extracted candidates
	Pass                    string            // the option that passes
}

// Choice records who answered a question, and on what input, so the workflow
// can reuse the answer and show where it came from.
type Choice struct {
	Question     string             `json:"question"`
	Candidates   map[string]string  `json:"candidates"`
	Answer       string             `json:"answer,omitempty"`
	P            map[string]float64 `json:"p,omitempty"`
	Model        string             `json:"model,omitempty"`
	InputHash    string             `json:"input_hash"`             // sha256 of the state, instructions and options
	Judge        string             `json:"judge"`                  // "jev" | "reviewer" | "fallback"
	Independence string             `json:"independence,omitempty"` // "different_family" | "same_family"
	JudgeHarness string             `json:"judge_harness,omitempty"`
	JudgeModel   string             `json:"judge_model,omitempty"` // the model the judging run reported
}

// Retry is shared by every question in one gate: Max bounds the waiting summed
// across them, so the whole gate stays inside its lease.
type Retry struct {
	Waits []time.Duration
	Max   time.Duration
	Sleep func(context.Context, time.Duration) error
	spent time.Duration
}

// NewRetry retries twice, three minutes apart, and waits six minutes at most per gate.
func NewRetry() *Retry {
	return &Retry{Waits: []time.Duration{3 * time.Minute, 3 * time.Minute}, Max: 6 * time.Minute, Sleep: sleep}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// inputHash identifies what Jev was asked; JSON sorts the option keys.
func inputHash(q Question) string {
	b, _ := json.Marshal(struct {
		State, Instructions string
		Options             map[string]string
	}{q.State, q.Instructions, q.Options})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Ask puts q to Jev. When Jev is off, or still fails after the retries the
// shared budget allows, the reviewer stands in: the check waits for them and
// the choice is recorded as "fallback".
func Ask(ctx context.Context, j *jev.Client, q Question, bands board.Bands, r *Retry) (board.Check, Choice) {
	ch := board.Check{ID: q.ID, Question: q.Instructions}
	c := Choice{Question: q.ID, Candidates: q.Options, InputHash: inputHash(q)}
	fallback := func(note string) (board.Check, Choice) {
		ch.Judge, ch.Result, ch.Note = board.Reviewer, board.Attention, note
		c.Judge = "fallback"
		return ch, c
	}
	if j == nil {
		return fallback("jev egress is off: the reviewer answers")
	}
	for attempt := 0; ; attempt++ {
		a, err := j.Choice(ctx, q.State, q.Instructions, q.Options)
		if err == nil {
			p := a.Probabilities[q.Pass]
			ch.Judge, ch.Model, ch.Result, ch.P, ch.Choice = board.Jev, j.Model, bands.Result(p), &p, a.Choice
			c.Judge, c.Model, c.Answer, c.P = "jev", j.Model, a.Choice, a.Probabilities
			return ch, c
		}
		if attempt >= len(r.Waits) || r.spent+r.Waits[attempt] > r.Max {
			return fallback(fmt.Sprintf("jev unavailable after %d attempts: %v", attempt+1, err))
		}
		r.spent += r.Waits[attempt]
		if serr := r.Sleep(ctx, r.Waits[attempt]); serr != nil {
			return fallback(fmt.Sprintf("jev unavailable after %d attempts: %v", attempt+1, err))
		}
	}
}

// ChoicePut is the record that keeps a choice, keyed by its question.
func ChoicePut(c Choice) records.Put { return records.Put{Kind: "choice", Key: c.Question, Body: c} }

// ReviewItem is the reviewer's answer to one check left for them.
type ReviewItem struct {
	Check  string `json:"check"`
	Result string `json:"result"`
	Choice string `json:"choice,omitempty"`
	Note   string `json:"note"`
}

// Verdict is the reviewer's verdict on the whole gate.
type Verdict struct {
	Result string `json:"result"`
	Step   string `json:"step,omitempty"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason"`
}

// Units are what one observation in a dataset can represent. The choice sets
// what later columns may count as n.
var Units = map[string]string{
	"biological_replicates": "Each observation is an independently grown or collected biological unit: organism, culture, donor or patient",
	"technical_repeats":     "Observations are repeated measurements of the same biological sample",
	"paired_samples":        "Observations come in matched pairs or repeated measures of the same subject under different conditions",
	"shared_calibration":    "Observations are linked by a shared calibration standard or reference sample",
	"unclear":               "The metadata does not say what one observation represents",
}

// Finalize folds reviewer answers and verdict into the checks, then decides.
func Finalize(v *records.View, g *board.Gate) {
	settle(v, g)
	vc := board.Check{ID: "review.verdict", Judge: board.Reviewer, Result: board.Fail, Note: "the reviewer gave no verdict"}
	if r, ok := v.Latest("verdict"); ok {
		vd := records.Body[Verdict](r)
		vc.Result, vc.Owner, vc.To, vc.Note = board.Result(vd.Result), vd.Step, vd.To, vd.Reason
	}
	g.Checks = append(g.Checks, vc)
	board.Decide(g)
}

// Settle folds reviewer answers into the checks left in the middle band, then
// decides. The reviewer gives no verdict of its own, so it can fail the gate
// only through a check it was asked to settle.
func Settle(v *records.View, g *board.Gate) {
	settle(v, g)
	board.Decide(g)
}

func settle(v *records.View, g *board.Gate) {
	items := map[string]ReviewItem{}
	for _, r := range v.Kind("review_item") {
		it := records.Body[ReviewItem](r)
		items[it.Check] = it
	}
	for i := range g.Checks {
		ch := &g.Checks[i]
		it, ok := items[ch.ID]
		if !ok || ch.Result != board.Attention {
			continue
		}
		ch.Result = board.Result(it.Result)
		ch.Note = strings.TrimPrefix(ch.Note+"; reviewer: "+it.Note, "; ")
		if it.Choice != "" {
			ch.Choice = it.Choice
		}
		if ch.Choice == "unclear" || (strings.HasPrefix(ch.ID, "unit.") && Units[ch.Choice] == "") {
			ch.Result = board.Fail
		}
	}
}
