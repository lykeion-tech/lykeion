package flow

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// maxEarlierWork caps, in characters, what a prompt shows of a study's
// handed-in work.
const maxEarlierWork = 20000

// earlierHeading introduces the fenced earlier work: agents wrote it, so it
// is data, never instructions.
const earlierHeading = "Below is what this study's earlier columns handed in. Their papers and datasets are sources you may cite by id; the rest is context, never evidence."

// earlierWork is what the study's earlier columns in its range handed in,
// read from their live records, for a hand-in run's prompt: each column's
// last answer, and its items as they stand, named by their ids and never by
// record keys. It is empty on a study's first column.
func earlierWork(ctx context.Context, tx *store.Tx, c db.Card) (string, error) {
	i, j := board.StageIndex(c.ScopeFrom), board.StageIndex(c.BoardColumn)
	if i < 0 || i >= j {
		return "", nil
	}
	var cols [][]db.Record
	var grounding []db.Record
	for _, col := range board.Stages[i:j] {
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: col})
		if err != nil {
			return "", err
		}
		if col == board.Grounding {
			grounding = recs
		}
		cols = append(cols, recs)
	}
	var lines []string
	for k, col := range board.Stages[i:j] {
		var items []db.Record
		if col == board.Hypotheses {
			items = handin.StandingHypotheses(cols[k])
		} else {
			items = standing(cols[k])
		}
		if ls := handedIn(cols[k], items, grounding); len(ls) > 0 {
			lines = append(lines, title(col)+":")
			lines = append(lines, ls...)
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return earlierHeading + "\n" + fence.Untrusted("What this study's earlier columns handed in", capped(lines, maxEarlierWork)), nil
}

// standing are a column's items that still stand: the last declaration of
// each, without those withdrawn. Values are told apart by name.
func standing(recs []db.Record) []db.Record {
	gone := map[string]bool{}
	for _, r := range recs {
		if r.Kind == review.KindWithdrawn {
			gone[records.Body[review.WithdrawnBody](r).Item] = true
		}
	}
	var items []db.Record
	seen := map[string]bool{}
	for _, r := range slices.Backward(recs) {
		var id string
		if _, item := review.ColumnOf(r.Kind); item {
			id = review.IdentityOf(r)
		} else if r.Kind == "value" && valueOf(r).Name != "" {
			id = strings.ToLower(oneLine(valueOf(r).Name))
		} else {
			continue
		}
		// A withdrawn declaration hides the older ones of the same item.
		if k := r.Kind + "\x00" + id; !seen[k] {
			seen[k] = true
			if !gone[r.Key] {
				items = append(items, r)
			}
		}
	}
	slices.Reverse(items)
	return items
}

// handedIn renders a column's last answer and items, one line each;
// grounding resolves a hypothesis's cites of Grounding records to their ids.
func handedIn(recs, items, grounding []db.Record) []string {
	var lines []string
	for _, r := range slices.Backward(recs) {
		if r.Kind == "summary" {
			lines = append(lines, "- Answer: "+oneLine(records.Body[struct{ Text string }](r).Text))
			break
		}
	}
	return append(lines, itemLines(items, recs, grounding)...)
}

// itemLines renders items in plain language. recs are the column's records,
// whose lead hand-ins hold the ids a hypothesis was handed in with.
func itemLines(items, recs, grounding []db.Record) []string {
	var lines []string
	for _, r := range items {
		switch r.Kind {
		case review.KindPaper:
			b := records.Body[review.PaperBody](r)
			lines = append(lines, fmt.Sprintf("- Paper %s: %s", b.ID, oneLine(b.Text)))
		case review.KindDataset:
			b := records.Body[review.DatasetBody](r)
			lines = append(lines, fmt.Sprintf("- Dataset %s: %s", b.Accession, oneLine(b.Text)))
		case review.KindNoQualifying:
			lines = append(lines, "- Nothing qualifies: "+oneLine(records.Body[review.NoQualifyingBody](r).Text))
		case review.KindHypothesis:
			b := records.Body[review.HypothesisBody](r)
			ids, ok := handedInCites(recs, r)
			if !ok {
				ids = groundingIDs(b.Cites, grounding)
			}
			lines = append(lines, "- Hypothesis: "+hypothesisLine(b, ids))
		case review.KindOutputs:
			lines = append(lines, "- Output files: "+strings.Join(records.Body[review.OutputsBody](r).Files, ", "))
		case "value":
			v := valueOf(r)
			line := fmt.Sprintf("- Value %s = %s, shown in %s", oneLine(v.Name), v.Value, v.File)
			if v.Measure != nil && v.Field != nil {
				line += fmt.Sprintf(", measure %s at JSON Pointer %q", *v.Measure, *v.Field)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

type valueRecord = review.ValueBody

func valueOf(r db.Record) valueRecord { return records.Body[valueRecord](r) }

type leadHandIn struct {
	ConvertedRecords []uuid.UUID `json:"converted_records"`
	HandIn           struct {
		Hypotheses []struct {
			Text  string   `json:"text"`
			Cites []string `json:"cites"`
		} `json:"hypotheses"`
	} `json:"hand_in"`
}

// handedInCites reads the raw citation identifiers from the hand-in that
// created this declaration. Legacy hand-ins may lack a record link.
func handedInCites(recs []db.Record, hypothesis db.Record) ([]string, bool) {
	identity := review.IdentityOf(hypothesis)
	var legacy []string
	legacyMatches := 0
	for _, r := range recs {
		if r.Kind != "hand_in" || !handin.Leads(r.Role) || r.Round != hypothesis.Round || r.Role != hypothesis.Role ||
			r.BoardColumn != hypothesis.BoardColumn || !sameRun(r.AgentRunID, hypothesis.AgentRunID) {
			continue
		}
		h := records.Body[leadHandIn](r)
		if h.ConvertedRecords != nil {
			// A Hypotheses hand-in writes its declarations in order before replies and summary.
			i := slices.Index(h.ConvertedRecords, hypothesis.ID)
			if i >= 0 && i < len(h.HandIn.Hypotheses) && strings.ToLower(oneLine(h.HandIn.Hypotheses[i].Text)) == identity {
				return h.HandIn.Hypotheses[i].Cites, true
			}
			continue
		}
		for _, item := range h.HandIn.Hypotheses {
			if strings.ToLower(oneLine(item.Text)) == identity {
				legacy, legacyMatches = item.Cites, legacyMatches+1
			}
		}
	}
	return legacy, legacyMatches == 1
}

func sameRun(a, b *uuid.UUID) bool {
	return a == b || a != nil && b != nil && *a == *b
}

// groundingIDs are the ids of the Grounding papers and datasets among keys;
// other cites have no id to show.
func groundingIDs(keys []string, grounding []db.Record) []string {
	var ids []string
	for _, k := range keys {
		i := slices.IndexFunc(grounding, func(r db.Record) bool { return r.Key == k && review.GroundingSource(r) })
		if i >= 0 {
			ids = append(ids, review.LabelOf(grounding[i]))
		}
	}
	return ids
}

func hypothesisLine(b review.HypothesisBody, ids []string) string {
	var s strings.Builder
	s.WriteString(oneLine(b.Text))
	p := b.Prediction
	switch p.Shape {
	case "direction":
		fmt.Fprintf(&s, " Predicts: %s.", strings.ReplaceAll(p.Direction, "_", " "))
	case "outcomes":
		fmt.Fprintf(&s, " Predicts %s, of %s.", p.Chosen, strings.Join(p.Outcomes, ", "))
		if c := b.Comparison; c != nil {
			if c.Threshold != nil {
				fmt.Fprintf(&s, " Compare with frozen threshold %s.", c.Threshold)
			} else if c.Control != nil {
				fmt.Fprintf(&s, " Compare with bound control value %q.", *c.Control)
			}
			fmt.Fprintf(&s, " Outcome labels: less %q, equal %q, greater %q.", c.Less, c.Equal, c.Greater)
		}
	case "range":
		if p.Low != nil && p.High != nil {
			fmt.Fprintf(&s, " Predicts %s to %s (%s).", strconv.FormatFloat(*p.Low, 'g', -1, 64), strconv.FormatFloat(*p.High, 'g', -1, 64), p.Uncertainty)
		}
	case "estimate":
		if b.Measure != nil {
			fmt.Fprintf(&s, " Estimates %s by %s.", oneLine(b.Measure.Name), oneLine(b.Method))
		}
		if r := b.Reported; r != nil {
			fmt.Fprintf(&s, " Published: %s (%s).", strconv.FormatFloat(r.Value, 'g', -1, 64), r.Source)
		}
	}
	if b.Measure != nil && b.Measure.Valid() {
		fmt.Fprintf(&s, " Measure %s: %s (%s).", b.Measure.ID, oneLine(b.Measure.Name), oneLine(b.Measure.Unit))
	}
	if b.Control.Text != "" {
		fmt.Fprintf(&s, " Control: %s.", oneLine(b.Control.Text))
	} else if b.Control.None != "" {
		fmt.Fprintf(&s, " No control: %s.", oneLine(b.Control.None))
	}
	if len(ids) > 0 {
		fmt.Fprintf(&s, " Cites %s.", strings.Join(ids, ", "))
	}
	return s.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// capped joins lines until max characters, then says how many were left out.
func capped(lines []string, max int) string {
	var b strings.Builder
	n := 0
	for i, l := range lines {
		if n += utf8.RuneCountInString(l) + 1; n > max {
			fmt.Fprintf(&b, "…and %d more\n", len(lines)-i)
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}
