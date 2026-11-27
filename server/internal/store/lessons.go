package store

import (
	"context"

	"github.com/google/uuid"
)

func (t *Tx) LessonQuestion(ctx context.Context, card uuid.UUID) (string, error) {
	var q string
	err := t.raw.QueryRow(ctx, `SELECT concat_ws(E'\n',c.question,(SELECT string_agg(h.text,E'\n' ORDER BY h.id) FROM (
 SELECT id,left(body->>'text',2048) AS text FROM record WHERE workspace_id=c.workspace_id AND card_id=c.id
 AND kind='hypothesis' AND superseded_at IS NULL AND coalesce(body->>'server','')<>'attachments' ORDER BY id LIMIT 64) h))
 FROM card c WHERE workspace_id=$1 AND id=$2`, t.WS, card).Scan(&q)
	return q, err
}
