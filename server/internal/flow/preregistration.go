package flow

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"lykeion/server/internal/predicate"
)

// Predicate is one pre-registered predicate of a study card.
type Predicate struct{ Name, Text string }

// RunResults is what one run of the study's Experimentation column wrote.
type RunResults struct {
	Key, Tool    string
	ExitStatus   int
	Results      map[string]float64
	ResultsError string
}

// OtherRun is another counting run that wrote some of a predicate's keys.
type OtherRun struct {
	Run    string             `json:"run"`
	Values map[string]float64 `json:"values"`
}

// Decision is the outcome of one predicate.
type Decision struct {
	Name    string             `json:"name"`
	Text    string             `json:"text"`
	Outcome string             `json:"outcome"` // "held", "missed" or "unconfirmed"
	Round   int32              `json:"round"`   // round it was decided on; 0 while unconfirmed
	Run     string             `json:"run"`     // run key the values came from
	Values  map[string]float64 `json:"values"`  // the keys the predicate reads
	Others  []OtherRun         `json:"others"`  // other counting runs that wrote any of those keys
	Reason  string             `json:"reason"`  // why unconfirmed
}

func (r RunResults) counts() bool { return r.Tool == "run_script" && r.ExitStatus == 0 }

// compareRunKeys orders run keys (x2 before x10).
func compareRunKeys(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b))
}

// Decide settles each predicate from the named run. A predicate already
// held or missed in prior is carried over unchanged.
func Decide(preds []Predicate, prior []Decision, named string, runs []RunResults, round int32) []Decision {
	out := make([]Decision, 0, len(preds))
	for _, p := range preds {
		if i := slices.IndexFunc(prior, func(d Decision) bool {
			return d.Name == p.Name && (d.Outcome == "held" || d.Outcome == "missed")
		}); i >= 0 {
			out = append(out, prior[i])
			continue
		}
		out = append(out, decideOne(p, named, runs, round))
	}
	return out
}

func decideOne(p Predicate, named string, runs []RunResults, round int32) Decision {
	d := Decision{Name: p.Name, Text: p.Text, Outcome: "unconfirmed", Others: []OtherRun{}}
	unconfirmed := func(format string, args ...any) Decision {
		d.Reason = fmt.Sprintf(format, args...)
		return d
	}
	pred, err := predicate.Parse(p.Text)
	if err != nil {
		return unconfirmed("%s", err.Error())
	}
	keys := pred.Keys()
	if named == "" {
		return unconfirmed("no results run named")
	}
	i := slices.IndexFunc(runs, func(r RunResults) bool { return r.Key == named })
	if i < 0 {
		return unconfirmed("run %s is not a run of this column", named)
	}
	run := runs[i]
	d.Run = named

	// Other counting runs are listed even when the named run is unusable.
	others := slices.Clone(runs)
	slices.SortFunc(others, func(a, b RunResults) int { return compareRunKeys(a.Key, b.Key) })
	for _, o := range others {
		if o.Key == named || !o.counts() {
			continue
		}
		values := pick(o.Results, keys)
		if len(values) > 0 {
			d.Others = append(d.Others, OtherRun{Run: o.Key, Values: values})
		}
	}

	switch {
	case run.Tool != "run_script":
		return unconfirmed("run %s is not a script run", named)
	case run.ExitStatus == -1:
		return unconfirmed("run %s timed out", named)
	case run.ExitStatus != 0:
		return unconfirmed("run %s exited %d", named, run.ExitStatus)
	case run.ResultsError != "":
		return unconfirmed("results file: %s", run.ResultsError)
	}
	for _, key := range keys {
		if _, ok := run.Results[key]; !ok {
			return unconfirmed("run %s is missing %s", named, key)
		}
	}
	held, err := pred.Eval(run.Results)
	if err != nil {
		return unconfirmed("%s", err.Error())
	}
	d.Values = pick(run.Results, keys)
	d.Round = round
	d.Outcome = "missed"
	if held {
		d.Outcome = "held"
	}
	return d
}

func pick(from map[string]float64, keys []string) map[string]float64 {
	out := map[string]float64{}
	for _, key := range keys {
		if v, ok := from[key]; ok {
			out[key] = v
		}
	}
	return out
}

// Binding folds preregistration record bodies (oldest first) into one
// decision per predicate: the first held or missed, else the latest
// unconfirmed. Names follow the latest readable body; unreadable bodies are
// skipped.
func Binding(bodies [][]byte) []Decision {
	var order []string
	best := map[string]Decision{}
	for _, raw := range bodies {
		var body struct {
			Predicates []Decision `json:"predicates"`
		}
		if json.Unmarshal(raw, &body) != nil {
			continue
		}
		order = order[:0]
		for _, d := range body.Predicates {
			order = append(order, d.Name)
			if cur, seen := best[d.Name]; seen && (cur.Outcome == "held" || cur.Outcome == "missed") {
				continue
			}
			best[d.Name] = d
		}
	}
	out := make([]Decision, 0, len(order))
	for _, name := range order {
		out = append(out, best[name])
	}
	return out
}
