package store

import (
	"context"
	"github.com/google/uuid"
)

type StudyReaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

func (t *Tx) RunReactions(ctx context.Context, run, user uuid.UUID) ([]StudyReaction, error) {
	rows, err := t.raw.Query(ctx, `SELECT emoji, count(*), bool_or(user_id=$3) FROM run_reaction WHERE workspace_id=$1 AND run_id=$2 GROUP BY emoji ORDER BY min(created_at), emoji`, t.WS, run, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StudyReaction{}
	for rows.Next() {
		var r StudyReaction
		if err := rows.Scan(&r.Emoji, &r.Count, &r.Mine); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *Tx) SetRunReaction(ctx context.Context, run, user uuid.UUID, emoji string, add bool) error {
	if add {
		_, err := t.raw.Exec(ctx, `INSERT INTO run_reaction(workspace_id,run_id,user_id,emoji) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, t.WS, run, user, emoji)
		return err
	}
	_, err := t.raw.Exec(ctx, `DELETE FROM run_reaction WHERE workspace_id=$1 AND run_id=$2 AND user_id=$3 AND emoji=$4`, t.WS, run, user, emoji)
	return err
}

func (t *Tx) StudyReactions(ctx context.Context, card, user uuid.UUID) ([]StudyReaction, error) {
	rows, err := t.raw.Query(ctx, `SELECT emoji, count(*), bool_or(user_id=$3) FROM study_reaction WHERE workspace_id=$1 AND card_id=$2 GROUP BY emoji ORDER BY min(created_at), emoji`, t.WS, card, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StudyReaction{}
	for rows.Next() {
		var r StudyReaction
		if err := rows.Scan(&r.Emoji, &r.Count, &r.Mine); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *Tx) SetStudyReaction(ctx context.Context, card, user uuid.UUID, emoji string, add bool) error {
	if add {
		_, err := t.raw.Exec(ctx, `INSERT INTO study_reaction(workspace_id,card_id,user_id,emoji) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, t.WS, card, user, emoji)
		return err
	}
	_, err := t.raw.Exec(ctx, `DELETE FROM study_reaction WHERE workspace_id=$1 AND card_id=$2 AND user_id=$3 AND emoji=$4`, t.WS, card, user, emoji)
	return err
}

// A nil snapshot denotes a run created before snapshots were introduced.
func (t *Tx) RunAttachmentSnapshot(ctx context.Context, run uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := t.raw.QueryRow(ctx, `SELECT attachment_snapshot FROM agent_run WHERE workspace_id=$1 AND id=$2`, t.WS, run).Scan(&ids)
	return ids, err
}

func (t *Tx) CopyRunAttachmentSnapshot(ctx context.Context, from, to uuid.UUID) error {
	_, err := t.raw.Exec(ctx, `UPDATE agent_run SET attachment_snapshot=(SELECT attachment_snapshot FROM agent_run WHERE workspace_id=$1 AND id=$2) WHERE workspace_id=$1 AND id=$3`, t.WS, from, to)
	return err
}
