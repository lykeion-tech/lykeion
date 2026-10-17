package round

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/gate"
	"lykeion/server/internal/records"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// ErrRevisionRefused is a revision the study's kind or its citation does not allow.
var ErrRevisionRefused = errors.New("revision refused")

// ErrNoRevision is a revert on a study whose starting point was never revised.
var ErrNoRevision = errors.New("this study's starting point was never revised")

// Revision is a stage's change to the study's starting point. Cites is the
// record id of the result that undermines it.
type Revision struct{ Text, Cites, Reason string }

func refuse(why string) error { return fmt.Errorf("%w: %s", ErrRevisionRefused, why) }

// Revise checks a revision a stage asks for and records it. The drift choice
// says whether the new text changes what the study asks: "changes" blocks the
// study, an unanswered choice leaves it for the reviewer and applies nothing,
// and any other answer applies it. Applying uses the cap and keeps every
// version as a starting_point record, the original under key 0. The card,
// records and inbox items are saved here; the items are returned for the caller.
func (e *Engine) Revise(ctx context.Context, tx *store.Tx, c db.Card, r Revision, drift gate.Choice) ([]board.Item, error) {
	switch {
	case c.StartKind == "claim":
		return nil, refuse("a claim is never revised")
	case c.StartKind == "dataset" && r.Reason != "question":
		return nil, refuse("a dataset study may have only its question revised")
	case strings.TrimSpace(r.Text) == "":
		return nil, refuse("a revision needs its new text")
	}
	const cite = "a revision cites a live record of the study: the result that undermines its starting point"
	id, err := uuid.Parse(r.Cites)
	if err != nil {
		return nil, refuse(cite)
	}
	if _, err := tx.Q.LiveCardRecord(ctx, db.LiveCardRecordParams{WorkspaceID: tx.WS, CardID: c.ID, ID: id}); errors.Is(err, pgx.ErrNoRows) {
		return nil, refuse(cite)
	} else if err != nil {
		return nil, err
	}
	s := board.FromCard(c)
	switch drift.Answer {
	case "changes":
		items := board.Block(&s, board.BlockDrift, "the revision changes what the study asks: "+r.Text)
		return items, e.save(ctx, tx, c, s, items, "card.escalated", map[string]any{"block": board.BlockDrift})
	case "":
		return nil, nil
	}
	s.RouteBacks++
	if s.RouteBacks > s.Cap {
		items := board.Block(&s, board.BlockCap, fmt.Sprintf("failure %d with a cap of %d, in %s: a revision of the starting point", s.RouteBacks, s.Cap, s.Column))
		return items, e.save(ctx, tx, c, s, items, "card.escalated", map[string]any{"block": board.BlockCap})
	}
	points, err := tx.Q.CardKindRecords(ctx, db.CardKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, Kind: "starting_point"})
	if err != nil {
		return nil, err
	}
	var b records.Batch
	if len(points) == 0 {
		b.Put = append(b.Put, records.Put{Kind: "starting_point", Key: "0", Body: map[string]string{"text": c.Question}})
	}
	b.Put = append(b.Put, records.Put{Kind: "starting_point", Key: strconv.Itoa(max(len(points), 1)),
		Body: map[string]string{"text": r.Text, "cites": r.Cites, "reason": r.Reason}})
	if err := b.Write(ctx, tx, c); err != nil {
		return nil, err
	}
	s.Question = r.Text
	items := []board.Item{{Kind: "revised", Text: "the starting point was revised: " + r.Text}}
	return items, e.save(ctx, tx, c, s, items, "card.revised", map[string]any{"cites": r.Cites, "reason": r.Reason})
}

// RevertQuestion restores the study's original starting point and returns it
// to Grounding. The cap stays used.
func RevertQuestion(ctx context.Context, tx *store.Tx, c db.Card, s *board.State) ([]board.Item, error) {
	points, err := tx.Q.CardKindRecords(ctx, db.CardKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, Kind: "starting_point"})
	if err != nil {
		return nil, err
	}
	for _, p := range points {
		if p.Key == "0" {
			s.Question = records.Body[struct{ Text string }](p).Text
			return board.RevertRevision(s), nil
		}
	}
	return nil, ErrNoRevision
}
