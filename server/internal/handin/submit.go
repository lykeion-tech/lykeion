package handin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// memberWorking is the problem a lead's hand-in meets while a member's run
// of its column has not ended.
const memberWorking = "a member is still working: wait for its result with member_result"

// HandIns takes the hand-ins daemons post for their runs.
type HandIns struct {
	Runs *service.AgentRuns
	// SentBack is what the review before the study's round sent back; nil
	// reads as nothing sent back.
	SentBack func(context.Context, *store.Tx, db.Card) (Review, error)
	// Autoresearch applies an Autoresearch run's hand-in. It is a field because
	// the autoresearch package imports this one.
	Autoresearch AutoresearchOps
}

// AutoresearchOps are the two things an Autoresearch run's hand-in does.
type AutoresearchOps interface {
	Report(ctx context.Context, tx *store.Tx, run uuid.UUID, text string) error
	OpenCardForAgentRun(ctx context.Context, tx *store.Tx, run uuid.UUID, title, question string) (db.Card, error)
}

type handInBody struct {
	AgentRunID uuid.UUID       `json:"agent_run_id"`
	Attempt    int32           `json:"attempt"`
	HandIn     json.RawMessage `json:"hand_in"`
}

// Submit checks the hand-in of run's attempt ref.Attempt and, when nothing is
// wrong with it, records it and the records it becomes, in one transaction
// with the study locked. Each problem is one plain line for the agent; with
// any, nothing is written. A hand-in already recorded for the attempt is
// accepted again and writes nothing.
func (s *HandIns) Submit(ctx context.Context, user uuid.UUID, ref service.RuntimeRef, run uuid.UUID, raw json.RawMessage) ([]string, error) {
	var probs []string
	err := s.Runs.OnAttempt(ctx, user, ref, run, func(tx *store.Tx, t db.AgentRun) error {
		k, ok := KindFor(t.Kind, t.Role, t.BoardColumn)
		if !ok {
			return service.ErrConflict
		}
		if k == Autoresearch {
			// A kept hand-in replayed after the run settled was applied
			// already when its report is written: accept it again.
			if t.Status != "running" {
				r, err := tx.Q.RunByAgentRun(ctx, db.RunByAgentRunParams{WorkspaceID: tx.WS, AgentRunID: t.ID})
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				if err == nil && r.Report != "" {
					return nil
				}
				return service.ErrConflict
			}
			var err error
			probs, err = s.autoresearch(ctx, tx, t.ID, raw)
			return err
		}
		if t.CardID == nil {
			probs = []string{"not yet supported"}
			return nil
		}
		w := service.Writer{Card: *t.CardID, AgentRun: t.ID, Column: t.BoardColumn, Role: t.Role, Round: t.Round}
		var study []db.Record
		for _, col := range board.Stages {
			recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: w.Card, BoardColumn: col})
			if err != nil {
				return err
			}
			study = append(study, recs...)
		}
		for _, r := range study {
			if r.Kind == "hand_in" && r.AgentRunID != nil && *r.AgentRunID == run && records.Body[handInBody](r).Attempt == t.Attempt {
				return nil
			}
		}
		if t.Status != "running" {
			return service.ErrConflict
		}
		h, shape := Parse(k, raw)
		if probs = shape; len(probs) > 0 {
			return nil
		}
		lead := Leads(t.Role)
		if lead {
			_, err := tx.Q.InFlightMemberRun(ctx, db.InFlightMemberRunParams{WorkspaceID: tx.WS, CardID: w.Card, BoardColumn: w.Column, Round: w.Round})
			switch {
			case err == nil:
				probs = append(probs, memberWorking)
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		}
		var prev Review
		if lead && s.SentBack != nil {
			c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: w.Card})
			if err != nil {
				return err
			}
			// The review a run answers is the one before its own round, even
			// when the study has moved on since.
			c.BoardColumn, c.Round = t.BoardColumn, t.Round
			if prev, err = s.SentBack(ctx, tx, c); err != nil {
				return err
			}
		}
		recs, more := Convert(k, lead, h, NewEvidence(study), prev)
		if probs = append(probs, more...); len(probs) > 0 {
			return nil
		}
		if _, err := records.WriteAs(ctx, tx, w, "hand_in", handInBody{AgentRunID: t.ID, Attempt: t.Attempt, HandIn: raw}); err != nil {
			return err
		}
		for _, r := range recs {
			if _, err := records.WriteAs(ctx, tx, w, r.Kind, r.Body); err != nil {
				return err
			}
		}
		return nil
	})
	return probs, err
}
