package service

import (
	"context"
	"encoding/json"

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

// Kind is one type of record a column's roles may write. The board registers
// kinds; the server checks the writer's role and body before storing.
type Kind struct {
	Roles     []string
	Validate  func(json.RawMessage) error
	KeyPrefix string
	Prepare   func(ctx context.Context, tx *store.Tx, w Writer, body json.RawMessage) (json.RawMessage, error)
}

// Invalid is a record rejected for its content; the API answers 400.
type Invalid string

func (e Invalid) Error() string { return string(e) }

// SupersedeRole retires live records without erasing the historical rows.
func SupersedeRole(ctx context.Context, tx *store.Tx, card uuid.UUID, column, role string) error {
	return tx.Q.SupersedeRoleRecords(ctx, db.SupersedeRoleRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: column, Role: role})
}
