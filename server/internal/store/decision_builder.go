package store

import (
	"context"

	"github.com/google/uuid"
)

func (t *Tx) SetDecisionBuilderRuntime(ctx context.Context, chat, user, runtime uuid.UUID) error {
	_, err := t.raw.Exec(ctx, `UPDATE lab_chat SET builder_runtime_id=$4 WHERE workspace_id=$1 AND id=$2 AND owner_id=$3 AND purpose='manual_decision_draft' AND builder_runtime_id IS NULL`, t.WS, chat, user, runtime)
	return err
}

func (t *Tx) LockDecisionDraft(ctx context.Context, chat, user uuid.UUID) error {
	var id uuid.UUID
	return t.raw.QueryRow(ctx, `SELECT id FROM lab_chat WHERE workspace_id=$1 AND id=$2 AND owner_id=$3 AND purpose='manual_decision_draft' AND deleted_at IS NULL FOR UPDATE`, t.WS, chat, user).Scan(&id)
}

func (t *Tx) DecisionDraftReceipt(ctx context.Context, chat, user uuid.UUID) (uuid.UUID, []byte, error) {
	var decision uuid.UUID
	var hash []byte
	err := t.raw.QueryRow(ctx, `SELECT decision_id,input_hash FROM decision_draft_receipt WHERE workspace_id=$1 AND chat_id=$2 AND owner_id=$3`, t.WS, chat, user).Scan(&decision, &hash)
	return decision, hash, err
}

func (t *Tx) InsertDecisionDraftReceipt(ctx context.Context, chat, user, decision uuid.UUID, hash []byte) error {
	_, err := t.raw.Exec(ctx, `INSERT INTO decision_draft_receipt(workspace_id,owner_id,chat_id,decision_id,input_hash) VALUES($1,$2,$3,$4,$5)`, t.WS, user, chat, decision, hash)
	return err
}

func (t *Tx) DecisionBuilderRuntime(ctx context.Context, chat, user uuid.UUID) (*uuid.UUID, error) {
	var id *uuid.UUID
	err := t.raw.QueryRow(ctx, `SELECT builder_runtime_id FROM lab_chat WHERE workspace_id=$1 AND id=$2 AND owner_id=$3 AND purpose='manual_decision_draft' AND deleted_at IS NULL`, t.WS, chat, user).Scan(&id)
	return id, err
}
