package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Kind describes the roles and validator for a board record.
type Kind struct {
	Roles    []string
	Validate func(json.RawMessage) error
}

// SupersedeRole retires live records without erasing the historical rows.
func SupersedeRole(ctx context.Context, tx *store.Tx, card uuid.UUID, column, role string) error {
	return tx.Q.SupersedeRoleRecords(ctx, db.SupersedeRoleRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: column, Role: role})
}
