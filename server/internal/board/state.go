// Package board names the board's columns and a study's states, and holds a
// person's steering of a study. It touches no database.
package board

import (
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

	Working = "working"
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
	Column, State, Flag, Ending, EndReason string
	HandoffNote                            string // why the next round redoes work
	BlockKind, Question                    string
	Rounds, Round                          int32
}

func FromCard(c db.Card) State {
	return State{Column: c.BoardColumn, State: c.State, Flag: c.Flag, Ending: c.Ending, EndReason: c.EndReason,
		HandoffNote: c.HandoffNote, Rounds: c.Rounds, Round: c.Round, BlockKind: c.BlockKind, Question: c.Question}
}

func (s State) Params(id, ws uuid.UUID) db.SaveCardBoardParams {
	return db.SaveCardBoardParams{BoardColumn: s.Column, State: s.State, Flag: s.Flag, Ending: s.Ending,
		EndReason: s.EndReason, Rounds: s.Rounds, Round: s.Round, HandoffNote: s.HandoffNote,
		BlockKind: s.BlockKind, Question: s.Question, ID: id, WorkspaceID: ws}
}
