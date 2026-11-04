package store

import (
	"context"

	"github.com/google/uuid"
)

func (t *Tx) SetDecisionBuilderRuntime(ctx context.Context, chat, user, runtime uuid.UUID) error {
	_, err := t.raw.Exec(ctx, `UPDATE lab_chat SET builder_runtime_id=$4 WHERE workspace_id=$1 AND id=$2 AND owner_id=$3 AND purpose='manual_decision_draft' AND builder_runtime_id IS NULL`, t.WS, chat, user, runtime)
	return err
}

func (t *Tx) DecisionBuilderRuntime(ctx context.Context, chat, user uuid.UUID) (*uuid.UUID, error) {
	var id *uuid.UUID
	err := t.raw.QueryRow(ctx, `SELECT builder_runtime_id FROM lab_chat WHERE workspace_id=$1 AND id=$2 AND owner_id=$3 AND purpose='manual_decision_draft' AND deleted_at IS NULL`, t.WS, chat, user).Scan(&id)
	return id, err
}
