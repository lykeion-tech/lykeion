package review

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

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

// clip is s cut to its first 80 characters.
func clip(s string) string {
	r := []rune(s)
	return string(r[:min(80, len(r))])
}

// Experimentation reviews an Experimentation column: the server checks that
// each declared output file was saved and that each reported value was
// traced, and Jev warns where a sentence misdescribes a traced number.
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
		out.Checks = []Check{{ID: "declared", By: "server", Detail: "no outputs record", Status: Fail}}
	} else {
		var b OutputsBody
		if err := json.Unmarshal(latest.Body, &b); err != nil {
			return Result{}, fmt.Errorf("record %s: %w", latest.Key, err)
		}
		out.Key = latest.Key
		out.Checks = []Check{{ID: "declared", By: "server", Status: Pass}}
		for _, f := range b.Files {
			if !slices.Contains(in.Saved, f) {
				out.Checks = append(out.Checks, Check{ID: "file:" + f, By: "server", Detail: "not saved with lyk output put", Status: Fail})
			}
		}
	}
	out.Status = ItemStatus(out.Checks)
	r.Items = append(r.Items, out)

	for _, v := range in.Values {
		it := Item{Key: v.Record, Kind: "value", Label: ValueLabel(v), Identity: ValueIdentity(v)}
		traced := Check{ID: "traced", By: "server", Detail: v.Where, Status: Pass}
		if !v.Found {
			traced.Detail, traced.Status = "not printed by a script run that succeeded, nor in its saved files", Fail
		}
		it.Checks = []Check{traced}
		if v.Found {
			qs := map[string]string{"described": describedQuestion}
			state := ItemState("Experimentation", "Value: "+v.Value+"\n"+v.Sentence, []string{v.Context}, MaxState)
			ps, err := a.Noul(ctx, state, qs)
			if err != nil {
				return Result{}, err
			}
			p, ok := ps["described"]
			if !ok {
				return Result{}, fmt.Errorf("jev gave no answer for described")
			}
			it.Checks = append(it.Checks, Check{ID: "described", By: "jev", Question: describedQuestion, P: &p, Status: StatusOf(p), Warning: true})
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
