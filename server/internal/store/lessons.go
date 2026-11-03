package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type LessonEvent struct {
	ID           int64
	Source, Type string
	Workspace    uuid.UUID
	Card         *uuid.UUID
}

// Serialize only the bounded database delivery step, never an external call.
// Multiple server processes cannot process the same event before its ack.
func (t *Tx) PendingLessonWork(ctx context.Context, after uuid.UUID) ([]LessonEvent, error) {
	if !t.IsMaintenance() {
		return nil, errors.New("lesson maintenance required")
	}
	var got bool
	if err := t.raw.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(1696707451,9)`).Scan(&got); err != nil || !got {
		return nil, err
	}
	rows, err := t.raw.Query(ctx, `SELECT id,source,type,workspace_id,card_id FROM lab_pending_workspace_work('lessons',$1,200) ORDER BY id,source`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[LessonEvent])
}
func (t *Tx) AckLessonWork(ctx context.Context, events, dirty []int64) error {
	_, err := t.raw.Exec(ctx, `SELECT lab_ack_work('lessons',$1,$2)`, events, dirty)
	return err
}
func (t *Tx) LessonSteer(ctx context.Context, id int64, card uuid.UUID) (oldCol, oldState, col, state string, ok bool, err error) {
	err = t.raw.QueryRow(ctx, `SELECT COALESCE(l.board_column,''),COALESCE(l.board_state,''),COALESCE(e.payload->>'column',''),COALESCE(e.payload->>'state','')
 FROM event e JOIN card c ON (c.workspace_id,c.id)=(e.workspace_id,e.card_id)
 LEFT JOIN lesson_card l ON (l.workspace_id,l.card_id)=(e.workspace_id,e.card_id)
 WHERE e.workspace_id=$1 AND e.card_id=$2 AND e.id=$3 AND e.type='card.steered'`, t.WS, card, id).Scan(&oldCol, &oldState, &col, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
		return
	}
	if err != nil {
		return
	}
	if col == "" {
		col = oldCol
	}
	if state == "" {
		state = oldState
	}
	_, err = t.raw.Exec(ctx, `INSERT INTO lesson_card(workspace_id,card_id,board_column,board_state) VALUES($1,$2,$3,$4)
 ON CONFLICT(workspace_id,card_id) DO UPDATE SET board_column=EXCLUDED.board_column,board_state=EXCLUDED.board_state`, t.WS, card, col, state)
	ok = err == nil
	return
}
func (t *Tx) LessonDecision(ctx context.Context, id int64, card uuid.UUID) (*uuid.UUID, error) {
	var decision uuid.UUID
	err := t.raw.QueryRow(ctx, `SELECT i.id FROM event e JOIN inbox_item i ON (e.workspace_id,e.card_id)=(i.workspace_id,i.card_id)
 AND e.payload->>'inbox_item_id'=i.id::text
 WHERE e.workspace_id=$1 AND e.id=$2 AND e.card_id=$3 AND e.type='inbox.resolved' AND i.status IN ('decided','expired')
 AND (jsonb_array_length(i.options)>0 OR i.question_set IS NOT NULL)`, t.WS, id, card).Scan(&decision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &decision, err
}
func (t *Tx) QueueLessonStudy(ctx context.Context, card uuid.UUID, decision *uuid.UUID, trigger string, due time.Time) error {
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

type LessonCites struct {
	ID    uuid.UUID
	Cites []byte
}

func (t *Tx) NextLessonWorkspace(ctx context.Context, after uuid.UUID) (uuid.UUID, error) {
	var ws uuid.UUID
	err := t.raw.QueryRow(ctx, `SELECT COALESCE(lab_next_lesson_workspace($1),'00000000-0000-0000-0000-000000000000'::uuid)`, after).Scan(&ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return ws, err
}
func (t *Tx) ActiveLessonPage(ctx context.Context, after uuid.UUID) ([]LessonCites, error) {
	rows, err := t.raw.Query(ctx, `SELECT id,cites FROM lesson WHERE workspace_id=$1 AND status='active' AND id>$2 ORDER BY id LIMIT 16 FOR UPDATE`, t.WS, after)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[LessonCites])
}
func (t *Tx) SetAsideChangedLesson(ctx context.Context, id uuid.UUID) error {
	_, err := t.raw.Exec(ctx, `WITH changed AS (
 UPDATE lesson SET status='set_aside',set_aside_reason='fact changed' WHERE workspace_id=$1 AND id=$2 AND status='active' RETURNING workspace_id,id)
 INSERT INTO lesson_job(workspace_id,lesson_id,triggers,due_at) SELECT workspace_id,id,ARRAY['fact changed'],now() FROM changed`, t.WS, id)
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
func (t *Tx) FinishedLessonStudies(ctx context.Context, card uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := t.raw.Query(ctx, `SELECT c.id FROM unnest($3::uuid[]) WITH ORDINALITY AS hit(id,n) JOIN card c ON c.id=hit.id
 WHERE c.workspace_id=$1 AND c.id<>$2 AND c.state='done' ORDER BY hit.n`, t.WS, card, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
