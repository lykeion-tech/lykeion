package ground

import (
	"context"
	"fmt"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/records"
	db "lykeion/server/pkg/db/generated"
)

// ReviewKinds are the reviewer's records; the gate's input digest leaves them
// out because they answer the gate rather than feed it.
var ReviewKinds = []string{"review_item", "verdict", "gate_draft", "gate"}

// Relation is how a passage bears on a claim, as a Jev Choice.
var Relation = map[string]string{
	"supports":     "The passage states the claim or directly implies that it is true",
	"challenges":   "The passage states the opposite of the claim or implies it is false",
	"says_nothing": "The passage does not address what the claim asserts, either way",
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

// Normalize folds case and runs of whitespace, so quotes match across line breaks.
func Normalize(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// CodeCheck is a check the server decides by code: pass when ok.
func CodeCheck(id, owner string, ok bool, note string) board.Check {
	r := board.Pass
	if !ok {
		r = board.Fail
	}
	return board.Check{ID: id, Judge: board.Code, Owner: owner, Result: r, Note: note}
}

// Check runs code checks and, when they pass, asks Jev to assess evidence and
// dataset structure. An undecided gate still needs reviewer settlement.
func Check(ctx context.Context, v *records.View, j *jev.Client) (*board.Gate, error) {
	g := &board.Gate{Column: board.Grounding, InputDigest: v.Digest(ReviewKinds...)}
	if r, ok := v.Latest("cannot_proceed"); ok {
		g.Verdict = board.CannotProceed
		g.Reason = records.Body[struct {
			Reason string `json:"reason"`
		}](r).Reason
		return g, nil
	}
	ds := v.Kind("dataset")
	if len(ds) == 0 {
		for _, r := range v.Kind("nothing_found") {
			if r.Role == DataScout {
				searched := false
				for _, search := range v.Kind("search") {
					body := records.Body[Search](search)
					if search.Role == DataScout && body.Database == "geo" && strings.TrimSpace(body.Query) != "" {
						searched = true
						break
					}
				}
				if !searched {
					g.Checks = []board.Check{CodeCheck("search.datasets", DataScout, false, "no recorded GEO search with a nonempty query")}
					board.Decide(g)
					return g, nil
				}
				g.Verdict = board.CannotProceed
				g.Reason = "no usable data: " + records.Body[struct {
					Reason string `json:"reason"`
				}](r).Reason
				return g, nil
			}
		}
	}
	_, hasQ := v.Latest("question")
	_, hasN := v.Latest("notes")
	ev := v.Kind("evidence")
	hasLiteratureEvidence := false
	for _, r := range ev {
		e := records.Body[Evidence](r)
		f, fetched := v.ByKey("fetch", e.SourceID)
		if r.Role == Literature && fetched && records.Body[Fetch](f).SourceKind == "" {
			hasLiteratureEvidence = true
			break
		}
	}
	g.Checks = []board.Check{
		CodeCheck("question.present", Scoper, hasQ, "no scoped question"),
		CodeCheck("evidence.present", Literature, hasLiteratureEvidence, "no actual literature-source evidence from the literature scout"),
		CodeCheck("notes.present", Methods, hasN, "no methods notes"),
	}
	for _, r := range ev {
		e := records.Body[Evidence](r)
		f, _ := v.ByKey("fetch", e.SourceID)
		txt, err := v.Text(f)
		if err != nil {
			return nil, err
		}
		q := CodeCheck("quote."+r.Key, r.Role, strings.Contains(Normalize(txt), Normalize(e.Quote)), "quote not found verbatim in the fetched text")
		q.Evidence = []string{"evidence#" + r.Key, f.BlobKey}
		g.Checks = append(g.Checks, q,
			CodeCheck("retracted."+r.Key, r.Role, !records.Body[Fetch](f).Retracted, e.SourceID+" is retracted"))
	}
	dbs := map[string]bool{}
	contrary := false
	for _, r := range v.Kind("search") {
		s := records.Body[Search](r)
		if s.Database == "openalex" || s.Database == "europepmc" {
			dbs[s.Database] = true
		}
		contrary = contrary || r.Role == Contrarian
	}
	g.Checks = append(g.Checks,
		CodeCheck("search.databases", Literature, len(dbs) >= 2, fmt.Sprintf("searched %d literature database(s), need 2", len(dbs))),
		CodeCheck("search.contrary", Contrarian, contrary, "the contrary search never ran"),
		CodeCheck("datasets.opened", DataScout, len(ds) > 0, "no dataset was opened"),
	)
	for _, ch := range g.Checks {
		if ch.Result != board.Pass {
			board.Decide(g)
			return g, nil
		}
	}
	jc, err := jevChecks(ctx, v, j, ev, ds)
	g.Checks = append(g.Checks, jc...)
	return g, err
}

func passage(text, quote string) string {
	t, q := Normalize(text), Normalize(quote)
	i := strings.Index(t, q)
	if i < 0 {
		return t
	}
	return t[max(0, i-600):min(len(t), i+len(q)+600)]
}

// Origins classify whether a passage reports a paper's own result, its own
// synthesis, or another study's work.
var Origins = map[string]string{
	"own_result":  "The passage reports a finding from the study this paper describes",
	"synthesis":   "The passage states a review's or meta-analysis's own synthesis",
	"others_work": "The passage describes findings from other, earlier studies",
}

var MetadataOrigins = map[string]string{
	"own_result":  "The passage is the depositor's direct description of this dataset, not an independently demonstrated mechanism or outcome",
	"synthesis":   "The passage states the depositor's own synthesis of this dataset's information",
	"others_work": "The passage attributes findings to other work and requires original-source review",
}

func jevChecks(ctx context.Context, v *records.View, j *jev.Client, ev, ds []db.Record) ([]board.Check, error) {
	var out []board.Check
	for _, r := range ev {
		e := records.Body[Evidence](r)
		f, _ := v.ByKey("fetch", e.SourceID)
		txt, err := v.Text(f)
		if err != nil {
			return nil, err
		}
		fetch := records.Body[Fetch](f)
		state := "passage: " + passage(txt, e.Quote)
		origins := Origins
		originQuestion := "Does the passage report this paper's own result or synthesis, or describe another study's work?"
		if fetch.SourceKind == "dataset_metadata" {
			origins = MetadataOrigins
			originQuestion = "Does the passage directly describe this dataset's depositor information, synthesize it, or attribute findings to other work?"
			state = fmt.Sprintf("source_kind: dataset_metadata\nsource_url: %s\ntruncated: %t\nThis is a metadata projection, not a complete dataset or paper. A capped projection cannot establish absence or completeness of the full deposit.\n\n%s", fetch.SourceURL, fetch.Truncated, state)
		}
		ch := board.Check{ID: "support." + r.Key, Owner: r.Role, Evidence: []string{"evidence#" + r.Key},
			Question: "Does the passage state the claim?"}
		out = append(out, Ask(ctx, j, ch, "claim: "+e.Claim+"\n\n"+state, Relation,
			func(a jev.Answer) (board.Result, float64) {
				return board.DefaultBands.Result(a.Probabilities["supports"]), a.Probabilities["supports"]
			}))
		oc := board.Check{ID: "origin." + r.Key, Owner: r.Role, Evidence: []string{"evidence#" + r.Key},
			Question: originQuestion}
		out = append(out, Ask(ctx, j, oc, state, origins,
			func(a jev.Answer) (board.Result, float64) {
				p := a.Probabilities["own_result"] + a.Probabilities["synthesis"]
				if p >= 0.70 {
					return board.Pass, p
				}
				return board.Attention, p
			}))
	}
	for _, r := range ds {
		structure, err := unitState(v, r)
		if err != nil {
			return nil, err
		}
		ch := board.Check{ID: "unit." + r.Key, Owner: DataScout, Evidence: []string{"dataset#" + r.Key},
			Question: "What does one observation (one sample) in this dataset represent?"}
		out = append(out, Ask(ctx, j, ch, structure, Units, func(a jev.Answer) (board.Result, float64) {
			if a.Choice == "unclear" {
				return board.Attention, a.Probabilities[a.Choice]
			}
			return board.DefaultBands.Result(a.Probabilities[a.Choice]), a.Probabilities[a.Choice]
		}))
	}
	return out, nil
}

// Structure is read from the open_dataset record for its accession because a
// dataset record stores metadata and does not carry the opened text blob.
func Structure(v *records.View, d db.Record) (string, error) {
	o, ok := v.ByKey("open_dataset", records.Body[Dataset](d).Accession)
	if !ok {
		return "", nil
	}
	return v.Text(o)
}

// unitState augments discovery text only with live metadata referenced by the
// server's matching dataset open record. Legacy opens retain their text alone.
func unitState(v *records.View, d db.Record) (string, error) {
	structure, err := Structure(v, d)
	if err != nil {
		return "", err
	}
	o, ok := v.ByKey("open_dataset", records.Body[Dataset](d).Accession)
	if !ok {
		return structure, nil
	}
	refs := records.Body[struct {
		MetadataSources []struct {
			SourceID string `json:"source_id"`
		} `json:"metadata_sources"`
	}](o).MetadataSources
	var state strings.Builder
	state.WriteString(structure)
	for _, ref := range refs {
		f, ok := v.ByKey("fetch", ref.SourceID)
		if !ok {
			continue
		}
		source := records.Body[Fetch](f)
		if source.SourceKind != "dataset_metadata" {
			continue
		}
		text, err := v.Text(f)
		if err != nil {
			return "", err
		}
		if text == "" {
			continue
		}
		fmt.Fprintf(&state, "\n\nsource_id: %s\nsource_kind: dataset_metadata\nsource_url: %s\ntruncated: %t\nThis is a metadata projection, not a complete dataset or paper. A capped projection cannot establish absence or completeness of the full deposit.\n\n%s", ref.SourceID, source.SourceURL, source.Truncated, text)
	}
	return state.String(), nil
}

// Ask leaves uncertain checks for a reviewer when Jev egress is disabled or
// Jev cannot provide an answer.
func Ask(ctx context.Context, j *jev.Client, ch board.Check, state string, opts map[string]string, judge func(jev.Answer) (board.Result, float64)) board.Check {
	if j == nil {
		ch.Judge, ch.Result, ch.Note = board.Reviewer, board.Attention, "jev egress is off: the reviewer answers"
		return ch
	}
	ch.Judge, ch.Model = board.Jev, j.Model
	a, err := j.Choice(ctx, state, ch.Question, opts)
	if err != nil {
		ch.Result, ch.Note = board.Attention, "jev unavailable: "+err.Error()
		return ch
	}
	result, probability := judge(a)
	ch.Result, ch.P = result, &probability
	ch.Choice = a.Choice
	return ch
}

// Finalize folds reviewer answers and verdict into the checks, then decides.
func Finalize(v *records.View, g *board.Gate) {
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
	vc := board.Check{ID: "review.verdict", Judge: board.Reviewer, Result: board.Fail, Note: "the reviewer gave no verdict"}
	if r, ok := v.Latest("verdict"); ok {
		vd := records.Body[Verdict](r)
		vc.Result, vc.Owner, vc.To, vc.Note = board.Result(vd.Result), vd.Step, vd.To, vd.Reason
	}
	g.Checks = append(g.Checks, vc)
	board.Decide(g)
}
