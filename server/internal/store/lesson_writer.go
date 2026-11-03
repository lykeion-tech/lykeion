package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type WriterJob struct {
	ID, Workspace uuid.UUID
	Card, Lesson  *uuid.UUID
	Triggers      []string
}
type BatchJob struct {
	ID, Workspace uuid.UUID
	Batch         string
}

func (t *Tx) DueWriterJobs(ctx context.Context, token string) ([]WriterJob, error) {
	rows, err := t.raw.Query(ctx, `SELECT id,workspace_id,card_id,lesson_id,triggers FROM due_lesson_jobs(50) ORDER BY due_at,id`)
	if err != nil {
		return nil, err
	}
	jobs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[WriterJob])
	if err != nil {
		return nil, err
	}
	claimed := make([]WriterJob, 0, len(jobs))
	for _, j := range jobs {
		if err = t.Workspace(ctx, j.Workspace); err != nil {
			return nil, err
		}
		// An abandoned claim becomes eligible again. The token fences late workers.
		var fresh WriterJob
		err = t.raw.QueryRow(ctx, `UPDATE lesson_job SET batch_id=$2,due_at=now()+interval '5 minutes' WHERE id=$1 AND workspace_id=app_workspace() AND status='queued' AND due_at<=now() RETURNING id,workspace_id,card_id,lesson_id,triggers`, j.ID, token).Scan(&fresh.ID, &fresh.Workspace, &fresh.Card, &fresh.Lesson, &fresh.Triggers)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, fresh)
	}
	return claimed, nil
}

type WriterInput struct {
	Versions map[string]string `json:"included"`
	Observed map[string]string `json:"observed"`
}

func (t *Tx) SaveWriterInput(ctx context.Context, j uuid.UUID, token string, input WriterInput, rewrite string) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	tag, err := t.raw.Exec(ctx, `INSERT INTO lesson_job_input(workspace_id,job_id,versions,rewrite_version)
 SELECT workspace_id,id,$3,$4 FROM lesson_job WHERE workspace_id=app_workspace() AND id=$1 AND batch_id=$2 AND status='queued'
 ON CONFLICT(job_id) DO UPDATE SET versions=EXCLUDED.versions,rewrite_version=EXCLUDED.rewrite_version`, j, token, raw, rewrite)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}
func (t *Tx) ChangeWriterJob(ctx context.Context, id uuid.UUID, old, batch, status string, delay int) error {
	_, err := t.raw.Exec(ctx, `UPDATE lesson_job SET batch_id=$3,status=$4,due_at=clock_timestamp()+$5::int*interval '1 second'
 WHERE workspace_id=app_workspace() AND id=$1 AND batch_id=$2`, id, old, batch, status, delay)
	return err
}
func (t *Tx) PollWriterJobs(ctx context.Context) ([]BatchJob, error) {
	// Claim one poll transactionally. Another process can poll a different batch
	// on its next tick. A failed poll or a crash only delays the next attempt.
	var lock bool
	if err := t.raw.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(1696707451,10)`).Scan(&lock); err != nil || !lock {
		return nil, err
	}
	rows, err := t.raw.Query(ctx, `SELECT * FROM pending_lesson_batch()`)
	if err != nil {
		return nil, err
	}
	jobs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[BatchJob])
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if err = t.Workspace(ctx, j.Workspace); err != nil {
			return nil, err
		}
		if err = t.ChangeWriterJob(ctx, j.ID, j.Batch, j.Batch, "submitted", 30); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}
func (t *Tx) LockWriterResult(ctx context.Context, id uuid.UUID, batch string) (*uuid.UUID, WriterInput, string, error) {
	var lesson *uuid.UUID
	var raw []byte
	var version string
	err := t.raw.QueryRow(ctx, `SELECT j.lesson_id,i.versions,i.rewrite_version FROM lesson_job j JOIN lesson_job_input i ON (i.workspace_id,i.job_id)=(j.workspace_id,j.id) WHERE j.workspace_id=app_workspace() AND j.id=$1 AND j.status='submitted' AND j.batch_id=$2 FOR UPDATE OF j`, id, batch).Scan(&lesson, &raw, &version)
	if err != nil {
		return nil, WriterInput{}, "", err
	}
	var input WriterInput
	err = json.Unmarshal(raw, &input)
	return lesson, input, version, err
}
func (t *Tx) ColumnRecords(ctx context.Context, card uuid.UUID, column string) ([]uuid.UUID, error) {
	rows, err := t.raw.Query(ctx, `SELECT id FROM record WHERE workspace_id=app_workspace() AND card_id=$1 AND board_column=$2 AND superseded_at IS NULL ORDER BY created_at DESC,id LIMIT 48`, card, column)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
func (t *Tx) RelevantLessons(ctx context.Context, cards []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := t.raw.Query(ctx, `SELECT id FROM lesson WHERE workspace_id=app_workspace() AND status='active' ORDER BY (studies && $1::uuid[]) DESC,written_at DESC,id LIMIT 12`, cards)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

type RewriteSource struct {
	Text            string
	Cites           []byte
	Flags, Counters []string
	Revision        string
}

func (t *Tx) RewriteSource(ctx context.Context, id uuid.UUID, lock bool) (RewriteSource, error) {
	var s RewriteSource
	query := `SELECT l.text,l.cites,ARRAY(SELECT left(f.reason,2048) FROM lesson_flag f WHERE f.workspace_id=l.workspace_id AND f.lesson_id=l.id ORDER BY f.created_at DESC,f.id LIMIT 32),
 ARRAY(SELECT c.ref FROM lesson_counter c WHERE c.workspace_id=l.workspace_id AND c.lesson_id=l.id ORDER BY c.created_at DESC,c.id LIMIT 32),
 concat_ws('/',l.written_at,l.set_aside_reason,(SELECT count(*) FROM lesson_flag WHERE workspace_id=l.workspace_id AND lesson_id=l.id),(SELECT count(*) FROM lesson_counter WHERE workspace_id=l.workspace_id AND lesson_id=l.id))
 FROM lesson l WHERE l.workspace_id=app_workspace() AND l.id=$1 AND l.status='set_aside'`
	if lock {
		// Take the lock first, then read flags and counters in a fresh snapshot.
		var locked uuid.UUID
		if err := t.raw.QueryRow(ctx, `SELECT id FROM lesson WHERE workspace_id=app_workspace() AND id=$1 AND status='set_aside' FOR UPDATE`, id).Scan(&locked); err != nil {
			return s, err
		}
	}
	err := t.raw.QueryRow(ctx, query, id).Scan(&s.Text, &s.Cites, &s.Flags, &s.Counters, &s.Revision)
	return s, err
}
func (t *Tx) WriteLesson(ctx context.Context, replace *uuid.UUID, kind, text string, cites []byte, studies []uuid.UUID, general bool, model string) (uuid.UUID, error) {
	var id uuid.UUID
	var err error
	if replace == nil {
		err = t.raw.QueryRow(ctx, `INSERT INTO lesson(workspace_id,kind,text,cites,studies,generalises,model,checked_at) VALUES(app_workspace(),$1,$2,$3,$4,$5,$6,clock_timestamp()) RETURNING id`, kind, text, cites, studies, general, model).Scan(&id)
	} else {
		err = t.raw.QueryRow(ctx, `UPDATE lesson SET kind=$1,text=$2,cites=$3,studies=$4,generalises=$5,model=$6,checked_at=clock_timestamp(),written_at=clock_timestamp(),swept_at=NULL,status='active',set_aside_reason='' WHERE workspace_id=app_workspace() AND id=$7 AND status='set_aside' RETURNING id`, kind, text, cites, studies, general, model, *replace).Scan(&id)
	}
	return id, err
}
func (t *Tx) DeleteRewrite(ctx context.Context, id uuid.UUID) error {
	_, err := t.raw.Exec(ctx, `DELETE FROM lesson WHERE workspace_id=app_workspace() AND id=$1 AND status='set_aside'`, id)
	return err
}
func (t *Tx) AddCounter(ctx context.Context, lesson uuid.UUID, ref string) error {
	_, err := t.raw.Exec(ctx, `INSERT INTO lesson_counter(workspace_id,lesson_id,ref) VALUES(app_workspace(),$1,$2) ON CONFLICT(lesson_id,ref) DO NOTHING`, lesson, ref)
	return err
}
func (t *Tx) CounterSetAside(ctx context.Context, id uuid.UUID) error {
	_, err := t.raw.Exec(ctx, `WITH changed AS (UPDATE lesson SET status='set_aside',set_aside_reason='counter-fact' WHERE workspace_id=app_workspace() AND id=$1 AND status='active' RETURNING workspace_id,id)
 INSERT INTO lesson_job(workspace_id,lesson_id,triggers,due_at) SELECT workspace_id,id,ARRAY['counter-fact'],now() FROM changed`, id)
	return err
}

func (t *Tx) LockActiveLesson(ctx context.Context, id uuid.UUID) error {
	var locked uuid.UUID
	return t.raw.QueryRow(ctx, `SELECT id FROM lesson WHERE workspace_id=app_workspace() AND id=$1 AND status='active' FOR UPDATE`, id).Scan(&locked)
}

// FailWriterAttempt fences on the same batch/claim as the failed attempt.
// The retry budget spans provider submit, collect and guard failures and is
// persisted independently of prompt snapshots (including across restarts).
func (t *Tx) FailWriterAttempt(ctx context.Context, id uuid.UUID, batch, retryStatus string, permanent bool) error {
	_, err := t.raw.Exec(ctx, `UPDATE lesson_job SET failures=failures+1,
 status=CASE WHEN $4 OR failures>=2 THEN 'failed' ELSE $3 END,
 batch_id=CASE WHEN $4 OR failures>=2 OR $3='queued' THEN '' ELSE batch_id END,
 due_at=clock_timestamp()+interval '5 minutes'
 WHERE workspace_id=app_workspace() AND id=$1 AND batch_id=$2 AND status IN ('queued','submitted')`, id, batch, retryStatus, permanent)
	return err
}
