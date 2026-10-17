package round

import (
	"context"
	"encoding/json"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/gate"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Family is the model family behind each harness. Cursor runs models of
// several families, so its family is unknown and never counts as different.
var Family = map[string]string{"claude": "anthropic", "codex": "openai", "antigravity": "google"}

const (
	DifferentFamily = "different_family"
	SameFamily      = "same_family"
)

// Independence says whether the judge's harness is of a known family that
// differs from every author's. An author of unknown family may share it, and
// with no authors on record nothing shows the judge to be independent.
func Independence(authors []string, judge string) string {
	f := Family[judge]
	if f == "" || len(authors) == 0 {
		return SameFamily
	}
	for _, a := range authors {
		if fa := Family[a]; fa == "" || fa == f {
			return SameFamily
		}
	}
	return DifferentFamily
}

// ReviewHarness picks the first online harness of another family than every
// author. Without one the reviewer runs on fallback, the settings harness for
// the role, in its fresh per-round session.
func ReviewHarness(authors []string, online []string, fallback string) (harness, independence string) {
	for _, h := range online {
		if Independence(authors, h) == DifferentFamily {
			return h, DifferentFamily
		}
	}
	return fallback, SameFamily
}

// split separates the judged runs into the authors' harnesses and the
// column's latest reviewer run, if one ran.
func split(judged []db.JudgedAgentRunsRow, reviewer string) (authors []string, rev *db.JudgedAgentRunsRow) {
	for i, t := range judged {
		if t.Role == reviewer {
			rev = &judged[i]
			continue
		}
		authors = append(authors, t.Harness)
	}
	return authors, rev
}

// reviewHarness is the harness the column's reviewer is queued on.
func (e *Engine) reviewHarness(ctx context.Context, tx *store.Tx, c db.Card, reviewer, fallback string) (string, error) {
	judged, err := tx.Q.JudgedAgentRuns(ctx, db.JudgedAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return "", err
	}
	online, err := tx.Q.OnlineHarnesses(ctx, db.OnlineHarnessesParams{WorkspaceID: tx.WS, OwnerID: c.OwnerID})
	if err != nil {
		return "", err
	}
	authors, _ := split(judged, reviewer)
	h, _ := ReviewHarness(authors, online, fallback)
	return h, nil
}

// recordIndependence notes how independent the reviewer was on every
// reviewer-judged check it settled through a review_item this round, and on
// every choice it answered this round, standing in for Jev or as the reviewer.
// A choice already annotated keeps its record.
func (e *Engine) recordIndependence(ctx context.Context, tx *store.Tx, c db.Card, judged []db.JudgedAgentRunsRow, reviewer string, checks []board.Check) error {
	authors, rev := split(judged, reviewer)
	if rev == nil {
		return nil
	}
	ind := Independence(authors, rev.Harness)
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
	if err != nil {
		return err
	}
	settled := map[string]bool{}
	for _, r := range recs {
		if r.Round != c.Round {
			continue
		}
		switch r.Kind {
		case "review_item":
			var it gate.ReviewItem
			if json.Unmarshal(r.Body, &it) == nil {
				settled[it.Check] = true
			}
		case "choice":
			var ch gate.Choice
			if json.Unmarshal(r.Body, &ch) != nil || (ch.Judge != "reviewer" && ch.Judge != "fallback") || ch.Independence != "" {
				continue
			}
			ch.Independence, ch.JudgeHarness, ch.JudgeModel = ind, rev.Harness, rev.Model
			b, err := json.Marshal(ch)
			if err != nil {
				return err
			}
			if err := tx.Q.SupersedeRecord(ctx, db.SupersedeRecordParams{WorkspaceID: tx.WS, ID: r.ID}); err != nil {
				return err
			}
			if _, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: r.BoardColumn,
				Round: r.Round, Kind: r.Kind, Role: r.Role, Key: r.Key, Body: b}); err != nil {
				return err
			}
		}
	}
	for i := range checks {
		if ch := &checks[i]; ch.Judge == board.Reviewer && settled[ch.ID] {
			ch.Note = strings.TrimPrefix(ch.Note+"; independence: "+ind, "; ")
		}
	}
	return nil
}
