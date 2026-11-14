package review

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
)

// ValueCheck is one reported value and where the server traced it.
type ValueCheck struct {
	Record   string // the work record's key
	Value    string
	Found    bool   // printed by a run_script run that exited 0, or in its saved files
	Where    string // "run:x3" or "output:o2"
	Sentence string // the work record's text, for Jev
	Context  string // up to 300 bytes around the match
	Textual  bool   // a categorical or other nonnumeric outcome
}

type ExperimentationInput struct {
	Declared []db.Record // outputs records of this round (latest wins)
	Saved    []string    // names of output files saved in this column
	Values   []ValueCheck
	Carried  []Item // value items that did not fail on an earlier attempt
	Attempt  int
}

// ValueLabel is the label of a reported value's item.
func ValueLabel(v ValueCheck) string {
	return clip(v.Value + ": " + v.Sentence)
}

// ValueIdentity is what makes two value items the same: the work record and
// the value it reports.
func ValueIdentity(v ValueCheck) string {
	return v.Record + " " + v.Value
}

const describedQuestion = "Does the sentence describe this number correctly: what it measures, its direction and its unit?"

// clip is s cut to fit 80 characters, ellipsis included, at the last word
// boundary (or mid-word when it has no space).
func clip(s string) string {
	r := []rune(s)
	if len(r) <= 80 {
		return s
	}
	cut := 79
	if r[79] != ' ' {
		for i := 78; i > 0; i-- {
			if r[i] == ' ' {
				cut = i
				break
			}
		}
	}
	return strings.TrimRight(string(r[:cut]), " ") + "…"
}

// Experimentation reviews an Experimentation column: the server checks that
// each declared output file was saved and that each reported value was
// traced, and Jev warns where a sentence misdescribes a traced value.
func Experimentation(ctx context.Context, a Asker, in ExperimentationInput) (Result, error) {
	r := Result{Column: board.Experimentation, Attempt: in.Attempt}

	out := Item{Kind: KindOutputs, Label: "Declared outputs", Identity: KindOutputs}
	var latest *db.Record
	for i := range in.Declared {
		if latest == nil || !in.Declared[i].CreatedAt.Before(latest.CreatedAt) {
			latest = &in.Declared[i]
		}
	}
	if latest == nil {
		// Values stand without a declared list; with neither there is nothing
		// to review and the empty hand-in is the reason.
		if len(in.Values) == 0 && len(in.Carried) == 0 {
			return r, nil
		}
	} else {
		var b OutputsBody
		if err := json.Unmarshal(latest.Body, &b); err != nil {
			return Result{}, fmt.Errorf("record %s: %w", latest.Key, err)
		}
		out.Key = latest.Key
		out.Checks = []Check{{ID: "declared", By: "server", Status: Pass}}
		for _, f := range b.Files {
			if !slices.Contains(in.Saved, f) {
				out.Checks = append(out.Checks, Check{ID: "file:" + f, By: "server", Detail: "not among the study's saved files", Status: Fail})
			}
		}
	}
	if latest != nil {
		out.Status = ItemStatus(out.Checks)
		r.Items = append(r.Items, out)
	}

	for _, v := range in.Values {
		it := Item{Key: v.Record, Kind: "value", Label: ValueLabel(v), Identity: ValueIdentity(v)}
		traced := Check{ID: "traced", By: "server", Detail: v.Where, Status: Pass}
		if !v.Found {
			traced.Detail, traced.Status = "not printed by a script run that succeeded, nor in its saved files", Fail
		}
		it.Checks = []Check{traced}
		if v.Found {
			question := describedQuestion
			if v.Textual {
				question = "Does the sentence describe this outcome correctly?"
			}
			qs := map[string]string{"described": question}
			state := ItemState("Experimentation", "Value: "+v.Value+"\n"+v.Sentence, []string{v.Context}, MaxState)
			ps, err := a.Noul(ctx, state, qs)
			if err != nil {
				return Result{}, err
			}
			p, ok := ps["described"]
			if !ok {
				return Result{}, fmt.Errorf("jev gave no answer for described")
			}
			status := StatusOf(p)
			p = stored(p)
			it.Checks = append(it.Checks, Check{ID: "described", By: "jev", Question: question, P: &p, Status: status, Warning: true})
		}
		it.Status = ItemStatus(it.Checks)
		r.Items = append(r.Items, it)
	}
	for _, c := range in.Carried {
		c.Carried = true
		r.Items = append(r.Items, c)
	}
	return r, nil
}
