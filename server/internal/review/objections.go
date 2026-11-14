package review

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"

	"lykeion/server/internal/fence"
)

// Objection is an `objection` record's body. The objector files Label, Type,
// Quote and Problem; until the review judges it, Server is empty and
// Upheld false. TargetKey is the key of the record the label stands for,
// and SameFamily says the objector ran on the same CLI family as the column.
type Objection struct {
	TargetKey  string  `json:"target_key"`
	Label      string  `json:"label"`
	Type       string  `json:"type"`
	Quote      *string `json:"quote"`
	Problem    string  `json:"problem"`
	Server     string  `json:"server"` // "ok", or why the server dropped it
	Checks     []Check `json:"checks"`
	Upheld     bool    `json:"upheld"`
	SameFamily bool    `json:"same_family"`
}

// objectorHandIn tells the objector what to look for and how to hand it in.
const objectorHandIn = "You are checking a finished study for errors. Below are its hypotheses, each under a label in an order that means nothing, " +
	"with its prediction, its control, and the status the server computed for it from the study's recorded values; then the names of its values and saved files. " +
	"Read any saved file with `read_output`, by its name.\n\n" +
	"Look for these errors:\n" +
	"- `wrong_test`: the statistical test does not fit the data or the prediction.\n" +
	"- `leakage`: information from the test data reached the fitting, or the outcome reached the inputs.\n" +
	"- `unit_mismatch`: numbers compared or combined in different units or scales.\n" +
	"- `missing_control`: the comparison lacks the control it needs.\n" +
	"- `overreach`: the status claims more than the results show.\n\n" +
	"Your final answer is your hand-in: `status` (`done`, or `could_not_do` when something outside the work blocked it; then `reason` says what, and is null otherwise), " +
	"`answer` (what you checked, in plain language), and `objections`, one per error found, each with:\n" +
	"- `target`: a label below (such as Rival A), a value's name or a saved file's name, exactly as listed;\n" +
	"- `type`: one of the errors above;\n" +
	"- `quote`: a passage copied word for word from the target, or null;\n" +
	"- `problem`: what is wrong, specific to this target.\n" +
	"An objection naming a target not listed here, or quoting text the target does not hold, is dropped. Hand in no objections when you find no error. Leave `decision` null.\n\n"

// rivalLabel is the i-th shuffled label: Rival A to Rival Z, then Rival AA.
func rivalLabel(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return "Rival " + s
}

// ObjectorBrief is the blind objector's prompt: the hypotheses shuffled by
// seed under neutral labels, each with its prediction and code status, and
// the names of the study's values and saved files. It holds no names of who
// did the work. labels maps each label to its hypothesis's text.
func ObjectorBrief(statuses []StatusResult, hs []HypothesisBody, values, outputs []string, seed int64) (string, map[string]string) {
	labels := map[string]string{}
	var b strings.Builder
	for _, r := range shuffled(statuses, hs, seed) {
		labels[r.label] = r.text
		fmt.Fprintf(&b, "%s\n%s\n", r.label, r.block)
	}
	fmt.Fprintf(&b, "Values: %s\nSaved files: %s\n", strings.Join(values, ", "), strings.Join(outputs, ", "))
	return objectorHandIn + fence.Untrusted("The study's hypotheses, values and saved files", b.String()), labels
}

// RivalTexts is the text the objector was shown under each label of
// ObjectorBrief with the same arguments: what an objection to that label is
// checked against.
func RivalTexts(statuses []StatusResult, hs []HypothesisBody, seed int64) map[string]string {
	out := map[string]string{}
	for _, r := range shuffled(statuses, hs, seed) {
		out[r.label] = r.block
	}
	return out
}

type rival struct{ label, text, block string }

// shuffled are the hypotheses shuffled by seed, each with its label and the
// block the brief shows under it.
func shuffled(statuses []StatusResult, hs []HypothesisBody, seed int64) []rival {
	byText := map[string]StatusResult{}
	for _, s := range statuses {
		byText[strings.TrimSpace(s.Hypothesis)] = s
	}
	var out []rival
	for i, j := range rand.New(rand.NewPCG(uint64(seed), 0)).Perm(len(hs)) {
		h := hs[j]
		var b strings.Builder
		fmt.Fprintf(&b, "Hypothesis: %s\n%s\n", h.Text, predictionLine(h.Prediction))
		if h.Control.None != "" {
			fmt.Fprintf(&b, "No control: %s\n", h.Control.None)
		} else {
			fmt.Fprintf(&b, "Control: %s\n", h.Control.Text)
		}
		s := byText[strings.TrimSpace(h.Text)]
		fmt.Fprintf(&b, "Status: %s\nWhy: %s\n", s.Status, strings.Join(s.Reasons, "; "))
		out = append(out, rival{label: rivalLabel(i), text: h.Text, block: b.String()})
	}
	return out
}

// objectionQuestions are Jev's two questions on an objection; both must pass
// for it to be upheld.
var objectionQuestions = map[string]string{
	"wrong":    "Is the record wrong in the way stated?",
	"specific": "Is the objection specific to this record rather than generic?",
}

// JudgeObjections checks each objection against texts, the text of every
// target the objector was shown by its label. The server check comes first:
// the target exists and any quote is in its text verbatim. Only an objection
// that passes it is put to Jev, and it is upheld when both questions pass.
func JudgeObjections(ctx context.Context, ask Asker, objs []Objection, texts map[string]string) ([]Objection, error) {
	out := make([]Objection, 0, len(objs))
	for _, o := range objs {
		o.Checks, o.Upheld = []Check{}, false
		text, ok := texts[strings.TrimSpace(o.Label)]
		switch {
		case !ok:
			o.Server = fmt.Sprintf("the target %q is not one the objector was shown", o.Label)
		case o.Quote != nil && !quoteIn(text, *o.Quote):
			o.Server = fmt.Sprintf("the quoted passage is not in %q", o.Label)
		default:
			o.Server = "ok"
		}
		if o.Server != "ok" {
			out = append(out, o)
			continue
		}
		item := fmt.Sprintf("Objection (%s) to %s: %s", o.Type, o.Label, o.Problem)
		if o.Quote != nil {
			item += "\nQuoted from it: " + *o.Quote
		}
		ps, err := ask.Noul(ctx, ItemState("Validation", item, []string{text}, MaxState), objectionQuestions)
		if err != nil {
			return nil, err
		}
		o.Upheld = true
		for _, id := range []string{"wrong", "specific"} {
			p, ok := ps[id]
			if !ok {
				return nil, fmt.Errorf("jev gave no answer for %s", id)
			}
			c := Check{ID: id, By: "jev", Question: objectionQuestions[id], Status: StatusOf(p)}
			p = stored(p)
			c.P = &p
			o.Checks = append(o.Checks, c)
			o.Upheld = o.Upheld && c.Status == Pass
		}
		out = append(out, o)
	}
	return out, nil
}
