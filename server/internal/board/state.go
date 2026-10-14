// Package board holds the rules of the board: what a gate decides and how a
// card moves because of it. It touches no database.
package board

import (
	"time"

	"github.com/google/uuid"

	db "lykeion/server/pkg/db/generated"
)

const (
	Backlog         = "backlog"
	Grounding       = "grounding"
	Hypotheses      = "hypotheses"
	Experimentation = "experimentation"
	Validation      = "validation"
	Reporting       = "reporting"
	Done            = "done"

	Working  = "working"
	InReview = "in_review"
	Ready    = "ready"

	Escalated   = "escalated"
	OutOfBudget = "out_of_budget"
)

// Stages are the five scientific columns, in board order.
var Stages = []string{Grounding, Hypotheses, Experimentation, Validation, Reporting}

func StageIndex(c string) int {
	for i, s := range Stages {
		if s == c {
			return i
		}
	}
	return -1
}

type State struct {
	Column, State, Flag, Ending, EndReason, Rerun string
	HandoffNote                                   string // why the next round redoes work
	Rounds, RouteBacks, Round                     int32
	Stale                                         []string
	BudgetUSD, SpentUSD                           float64 // SpentUSD is read, never saved
	Deadline                                      *time.Time
}

func FromCard(c db.Card) State {
	return State{Column: c.BoardColumn, State: c.State, Flag: c.Flag, Ending: c.Ending, EndReason: c.EndReason,
		Rerun: c.Rerun, HandoffNote: c.HandoffNote, Rounds: c.Rounds, RouteBacks: c.RouteBacks, Round: c.Round,
		Stale: c.Stale, BudgetUSD: c.BudgetUsd, SpentUSD: c.SpentUsd, Deadline: c.Deadline}
}

func (s State) Params(id, ws uuid.UUID) db.SaveCardBoardParams {
	stale := s.Stale
	if stale == nil {
		stale = []string{}
	}
	return db.SaveCardBoardParams{BoardColumn: s.Column, State: s.State, Flag: s.Flag, Ending: s.Ending,
		EndReason: s.EndReason, Rounds: s.Rounds, RouteBacks: s.RouteBacks, Stale: stale, Rerun: s.Rerun,
		Round: s.Round, BudgetUsd: s.BudgetUSD, HandoffNote: s.HandoffNote, ID: id, WorkspaceID: ws}
}
