package service

import (
	"context"
	"regexp"
	"slices"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Writer is the role writing a record.
type Writer struct {
	Card, AgentRun uuid.UUID
	Column, Role   string
	Round          int32
}

// ServerKinds are the record kinds only the server writes: no role command
// stores one, except the auditor's own `audit`.
var ServerKinds = []string{"delegation", "self_step", "summary", "audit", "score", "outcome", "call", "tools", "run", "output", "python_version"}

// FreeKinds are the agent-written kinds that need no citation; every other
// agent-written kind is a fact kind.
var FreeKinds = []string{"prediction", "explanation", "plan", "limitation"}

var kindName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// MayWrite says whether a run in role may store a record of kind through
// the records route: the auditor writes only its audit, every other role any
// well-formed kind but the server's.
func MayWrite(role, kind string) error {
	switch {
	case !kindName.MatchString(kind):
		return Invalid("a record kind is a lowercase name of at most 32 letters, digits and underscores")
	case role == "auditor":
		if kind != "audit" {
			return Invalid("the auditor writes only audit records")
		}
	case slices.Contains(ServerKinds, kind):
		return Invalid(kind + " records are written by the server")
	}
	return nil
}

// Invalid is a record rejected for its content; the API answers 400.
type Invalid string

func (e Invalid) Error() string { return string(e) }

// SupersedeRole retires live records without erasing the historical rows.
func SupersedeRole(ctx context.Context, tx *store.Tx, card uuid.UUID, column, role string) error {
	return tx.Q.SupersedeRoleRecords(ctx, db.SupersedeRoleRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: column, Role: role})
}
