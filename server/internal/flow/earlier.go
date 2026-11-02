package flow

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// maxEarlierWork caps, in characters, what a prompt shows of a study's
// handed-in work.
const maxEarlierWork = 20000

const earlierHeading = "What this study's earlier columns handed in. Their papers and datasets are sources you may cite by id; the rest is context, never evidence:"

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
		if ls := handedIn(cols[k], standing(cols[k]), grounding); len(ls) > 0 {
			lines = append(lines, title(col)+":")
			lines = append(lines, ls...)
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return earlierHeading + "\n" + capped(lines, maxEarlierWork), nil
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
		if gone[r.Key] {
			continue
		}
		var id string
		if _, item := review.ColumnOf(r.Kind); item {
			id = review.IdentityOf(r)
		} else if r.Kind == "value" && valueOf(r).Name != "" {
			id = strings.ToLower(oneLine(valueOf(r).Name))
		} else {
			continue
		}
		if k := r.Kind + "\x00" + id; !seen[k] {
			seen[k] = true
			items = append(items, r)
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
	cites := handedInCites(recs)
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
			ids, ok := cites[review.IdentityOf(r)]
			if !ok {
				ids = groundingIDs(b.Cites, grounding)
			}
			lines = append(lines, "- Hypothesis: "+hypothesisLine(b, ids))
		case review.KindOutputs:
			lines = append(lines, "- Output files: "+strings.Join(records.Body[review.OutputsBody](r).Files, ", "))
		case "value":
			v := valueOf(r)
			lines = append(lines, fmt.Sprintf("- Value %s = %s, shown in %s", oneLine(v.Name), v.Value, v.File))
		}
	}
	return lines
}

type valueRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	File  string `json:"file"`
}

func valueOf(r db.Record) valueRecord { return records.Body[valueRecord](r) }

type leadHandIn struct {
	HandIn struct {
		Hypotheses []struct {
			Text  string   `json:"text"`
			Cites []string `json:"cites"`
		} `json:"hypotheses"`
	} `json:"hand_in"`
}

// handedInCites maps each hypothesis a lead handed in, by identity, to the
// ids it cited.
func handedInCites(recs []db.Record) map[string][]string {
	out := map[string][]string{}
	for _, r := range recs {
		if r.Kind != "hand_in" || !slices.Contains([]string{"leader", "agent"}, r.Role) {
			continue
		}
		for _, h := range records.Body[leadHandIn](r).HandIn.Hypotheses {
			out[strings.ToLower(strings.Join(strings.Fields(h.Text), " "))] = h.Cites
		}
	}
	return out
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
	case "range":
		if p.Low != nil && p.High != nil {
			fmt.Fprintf(&s, " Predicts %s to %s (%s).", strconv.FormatFloat(*p.Low, 'g', -1, 64), strconv.FormatFloat(*p.High, 'g', -1, 64), p.Uncertainty)
		}
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
