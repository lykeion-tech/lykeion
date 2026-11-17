package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (t *Tx) QueueLessonStudy(ctx context.Context, card uuid.UUID, decision *uuid.UUID, trigger string, due time.Time) error {
	if decision != nil {
		var shared bool
		if err := t.raw.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inbox_item WHERE workspace_id=$1 AND id=$2 AND card_id=$3 AND kind<>'manual_decision' AND (jsonb_array_length(options)>0 OR question_set IS NOT NULL))`, t.WS, *decision, card).Scan(&shared); err != nil {
			return err
		}
		if !shared {
			return errors.New("shared lesson decision required")
		}
	}
	// Recheck status after taking the row lock: a batch writer can submit between ticks.
	var id uuid.UUID
	err := t.raw.QueryRow(ctx, `SELECT id FROM lesson_job WHERE workspace_id=$1 AND card_id=$2 AND lesson_id IS NULL AND status='queued' AND batch_id='' AND due_at > $3::timestamptz - interval '15 minutes'
 ORDER BY created_at,id LIMIT 1 FOR UPDATE`, t.WS, card, due).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = t.raw.Exec(ctx, `INSERT INTO lesson_job(workspace_id,card_id,decision_id,triggers,due_at) VALUES($1,$2,$3,ARRAY[$4]::text[],$5)`, t.WS, card, decision, trigger, due)
		return err
	}
	if err != nil {
		return err
	}
	_, err = t.raw.Exec(ctx, `UPDATE lesson_job SET triggers=CASE WHEN $3=ANY(triggers) THEN triggers ELSE array_append(triggers,$3) END,
 decision_id=COALESCE(decision_id,$4) WHERE workspace_id=$1 AND id=$2`, t.WS, id, trigger, decision)
	return err
}

func (t *Tx) LessonQuestion(ctx context.Context, card uuid.UUID) (string, error) {
	var q string
	err := t.raw.QueryRow(ctx, `SELECT concat_ws(E'\n',c.question,(SELECT string_agg(h.text,E'\n' ORDER BY h.id) FROM (
 SELECT id,left(body->>'text',2048) AS text FROM record WHERE workspace_id=c.workspace_id AND card_id=c.id
 AND kind='hypothesis' AND superseded_at IS NULL AND coalesce(body->>'server','')<>'attachments' ORDER BY id LIMIT 64) h))
 FROM card c WHERE workspace_id=$1 AND id=$2`, t.WS, card).Scan(&q)
	return q, err
}
