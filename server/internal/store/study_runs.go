package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RunGroup struct {
	CardID uuid.UUID `json:"card_id"`
	Stage  string    `json:"stage"`
	Round  int32     `json:"round"`
}

func (g RunGroup) Key() string {
	return g.CardID.String() + "~" + g.Stage + "~" + strconv.Itoa(int(g.Round))
}

type StudyRunRow struct {
	RunGroup
	ID             string     `json:"id"`
	Number         int32      `json:"number"`
	Title          string     `json:"title"`
	ProjectID      *uuid.UUID `json:"project_id"`
	ProjectName    string     `json:"project_name"`
	Status         string     `json:"status"`
	Worker         string     `json:"worker"`
	TeamName       *string    `json:"team_name"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	StopReason     string     `json:"-"`
	ElapsedSeconds *int64     `json:"elapsed_seconds"`
}

// Restrict eligible cards before grouping and paginate on the first enqueued run.
func (t *Tx) StudyRunGroups(ctx context.Context, project *uuid.UUID, group *RunGroup, before *time.Time, cursor RunGroup, limit int) ([]StudyRunRow, error) {
	var card *uuid.UUID
	var stage string
	var round int32
	if group != nil {
		card = &group.CardID
		stage = group.Stage
		round = group.Round
	}
	return t.studyRunRows(ctx, `SELECT * FROM groups g WHERE $6::timestamptz IS NULL OR
 (g.created,g.card_id,g.board_column,g.round)<($6,$7::uuid,$8::text,$9::int)
 ORDER BY created DESC,card_id DESC,board_column DESC,round DESC LIMIT $10`, t.WS, project, card, stage, round, before, cursor.CardID, cursor.Stage, cursor.Round, limit)
}

// Use the history feed's enqueue ordering, including its tie breakers, per study.
func (t *Tx) LatestStudyRuns(ctx context.Context) ([]StudyRunRow, error) {
	return t.studyRunRows(ctx, `SELECT DISTINCT ON (card_id) * FROM groups
 ORDER BY card_id, created DESC, board_column DESC, round DESC`, t.WS, nil, nil, "", 0)
}

func (t *Tx) studyRunRows(ctx context.Context, pageSQL string, args ...any) ([]StudyRunRow, error) {
	rows, err := t.raw.Query(ctx, `WITH groups AS (
 SELECT r.card_id,r.board_column,r.round,min(r.created_at) FILTER(WHERE r.kind='role' AND r.role IN ('leader','agent')) AS created,
 CASE WHEN NOT bool_or(r.legacy_unverified_started) FILTER(WHERE r.kind<>'report') THEN min(r.first_started_at) FILTER(WHERE r.kind<>'report') END AS started,
 CASE WHEN bool_and(r.completed_at IS NOT NULL) FILTER(WHERE r.kind<>'report')
 THEN max(r.completed_at) FILTER(WHERE r.kind<>'report') END AS ended,
 bool_or(r.kind<>'report' AND r.status='running') AS running,
 bool_or(r.kind<>'report' AND r.status='dispatched') AS dispatched,
 bool_or(r.kind<>'report' AND r.status='queued') AS queued,
 (array_agg(r.status ORDER BY r.created_at DESC,r.id DESC) FILTER(WHERE r.kind='role' AND r.role IN ('leader','agent')))[1] AS last_status,
 (array_agg(w.agent_name ORDER BY r.created_at DESC,r.id DESC) FILTER(WHERE r.kind='role' AND r.role IN ('leader','agent')))[1] AS worker,
 (array_agg(w.team_name ORDER BY r.created_at DESC,r.id DESC) FILTER(WHERE r.kind='role' AND r.role IN ('leader','agent')))[1] AS team_name
 FROM agent_run r JOIN card c ON c.workspace_id=r.workspace_id AND c.id=r.card_id
 LEFT JOIN study_run_worker w ON w.workspace_id=r.workspace_id AND w.run_id=r.id
 WHERE r.workspace_id=$1 AND r.kind IN ('role','rerun','report') AND r.card_id IS NOT NULL AND ($2::uuid IS NULL OR c.research_id=$2)
 AND r.board_column IN ('grounding','hypotheses','experimentation','validation') AND r.round>0
 AND ($3::uuid IS NULL OR (r.card_id,r.board_column,r.round)=($3,$4::text,$5::int))
 GROUP BY r.card_id,r.board_column,r.round HAVING bool_or(r.kind='role' AND r.role IN ('leader','agent'))
 ), page AS (`+pageSQL+`)
 SELECT g.card_id,g.board_column,g.round,c.number,c.title,c.research_id,coalesce(p.name,''),
 CASE WHEN c.state='stopped' AND c.round=g.round THEN 'cancelled'
 WHEN g.running THEN 'running' WHEN g.dispatched THEN 'dispatched' WHEN g.queued THEN 'queued'
 WHEN g.last_status IN ('failed','cancelled') THEN g.last_status
 WHEN (c.board_column,c.round)=(g.board_column,g.round) AND c.state='blocked' THEN 'blocked'
 WHEN (c.board_column,c.round)=(g.board_column,g.round) AND c.state<>'done' THEN 'reviewing'
 ELSE coalesce(g.last_status,'completed') END,
 coalesce(g.worker,''),g.team_name,g.created,g.started,g.ended,
 CASE WHEN c.state='stopped' AND c.round=g.round THEN c.end_reason ELSE '' END
 FROM page g JOIN card c ON c.workspace_id=$1 AND c.id=g.card_id
 LEFT JOIN research p ON p.workspace_id=c.workspace_id AND p.id=c.research_id
 ORDER BY g.created DESC,g.card_id DESC,g.board_column DESC,g.round DESC`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (StudyRunRow, error) {
		var g StudyRunRow
		err := row.Scan(&g.CardID, &g.Stage, &g.Round, &g.Number, &g.Title, &g.ProjectID, &g.ProjectName, &g.Status, &g.Worker, &g.TeamName, &g.CreatedAt, &g.StartedAt, &g.CompletedAt, &g.StopReason)
		g.ID = g.Key()
		switch g.Status {
		case "completed", "failed", "cancelled":
			if g.StartedAt != nil && g.CompletedAt != nil && !g.CompletedAt.Before(*g.StartedAt) {
				elapsed := int64(g.CompletedAt.Sub(*g.StartedAt) / time.Second)
				g.ElapsedSeconds = &elapsed
			}
		default:
			g.CompletedAt = nil
		}
		return g, err
	})
}

type StudyAttempt struct {
	Number      int32      `json:"number"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Status      string     `json:"status"`
}
type StudyParticipant struct {
	ID             uuid.UUID       `json:"id"`
	Role           string          `json:"role"`
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	AgentID        *uuid.UUID      `json:"agent_id"`
	TeamID         *uuid.UUID      `json:"team_id"`
	TeamName       *string         `json:"team_name"`
	Status         string          `json:"status"`
	StartedAt      *time.Time      `json:"started_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	Attempts       []StudyAttempt  `json:"attempts"`
	HandIn         json.RawMessage `json:"-"`
	TerminalReason string          `json:"-"`
	Waiting        string          `json:"-"`
}

func (t *Tx) StudyParticipantsForGroup(ctx context.Context, g RunGroup) ([]StudyParticipant, error) {
	rows, err := t.raw.Query(ctx, `SELECT r.id,r.role,r.kind,coalesce(w.agent_name,''),coalesce(w.agent_id,r.agent_id),w.team_id,w.team_name,r.status,r.first_started_at,r.completed_at,h.body->'hand_in',
 coalesce(nullif(r.cancel_reason,''),nullif(r.failure_reason,''),''),attempts.items,
 CASE WHEN r.status='queued' THEN coalesce(agent_run_waiting(r.workspace_id,r.id), CASE WHEN r.queue_reason='runtime_offline' THEN 'Waiting for a machine to come online.' END,'') ELSE '' END
 FROM agent_run r LEFT JOIN study_run_worker w ON w.workspace_id=r.workspace_id AND w.run_id=r.id
 LEFT JOIN LATERAL(SELECT b.body FROM record b WHERE b.workspace_id=r.workspace_id AND b.card_id=r.card_id AND b.agent_run_id=r.id
 AND b.board_column=r.board_column AND b.round=r.round AND b.kind='hand_in' AND b.superseded_at IS NULL
 AND b.body->>'attempt'=r.attempt::text ORDER BY b.created_at DESC,b.id DESC LIMIT 1) h ON true
 LEFT JOIN LATERAL (
 SELECT coalesce(jsonb_agg(jsonb_build_object('number',n,'started_at',coalesce(m.started_at,(a.result->>'started_at')::timestamptz,CASE WHEN n=r.attempt THEN r.started_at END),
 'completed_at',coalesce((a.result->>'completed_at')::timestamptz,CASE WHEN n=r.attempt THEN r.completed_at END),
 'status',coalesce(a.result->>'status',CASE WHEN n=r.attempt THEN r.status ELSE 'unknown' END)) ORDER BY n),'[]'::jsonb) AS items
 FROM (SELECT attempt AS n FROM agent_run_machine_attempt WHERE workspace_id=r.workspace_id AND agent_run_id=r.id
 UNION SELECT attempt FROM agent_run_receipt WHERE workspace_id=r.workspace_id AND agent_run_id=r.id
 UNION SELECT r.attempt WHERE r.attempt>0) nums
 LEFT JOIN agent_run_machine_attempt m ON m.workspace_id=r.workspace_id AND m.agent_run_id=r.id AND m.attempt=n
 LEFT JOIN agent_run_receipt a ON a.workspace_id=r.workspace_id AND a.agent_run_id=r.id AND a.attempt=n
 ) attempts ON true
 WHERE r.workspace_id=$1 AND r.card_id=$2 AND r.board_column=$3 AND r.round=$4 AND r.kind IN ('role','rerun','report') ORDER BY r.created_at,r.id`, t.WS, g.CardID, g.Stage, g.Round)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (StudyParticipant, error) {
		var p StudyParticipant
		err := row.Scan(&p.ID, &p.Role, &p.Kind, &p.Name, &p.AgentID, &p.TeamID, &p.TeamName, &p.Status, &p.StartedAt, &p.CompletedAt, &p.HandIn, &p.TerminalReason, &p.Attempts, &p.Waiting)
		return p, err
	})
}

type StudyRunOutput struct {
	ID        *uuid.UUID `json:"id"`
	Key       string     `json:"key"`
	Name      string     `json:"name"`
	RunID     uuid.UUID  `json:"run_id"`
	Available bool       `json:"available"`
	Blob      string     `json:"-"`
}

func (t *Tx) StudyRunOutputs(ctx context.Context, g RunGroup, id *uuid.UUID) ([]StudyRunOutput, error) {
	rows, err := t.raw.Query(ctx, `WITH runs AS (
 SELECT id,attempt,created_at,kind,role FROM agent_run WHERE kind IN ('role','rerun','report') AND workspace_id=$1 AND card_id=$2 AND board_column=$3 AND round=$4
 ), files AS (
 SELECT o.id,o.key,coalesce(nullif(o.body->>'name',''),o.key) AS name,r.id AS run_id,o.blob_key,o.created_at
 FROM record o JOIN runs r ON r.id=o.agent_run_id
 WHERE o.workspace_id=$1 AND o.card_id=$2 AND o.board_column=$3 AND o.round=$4 AND o.kind='output' AND o.superseded_at IS NULL
 ), declared AS (
 SELECT DISTINCT r.id AS run_id,decl.name,r.created_at FROM runs r JOIN record h ON h.agent_run_id=r.id
 CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(h.body->'hand_in'->'outputs')='array' THEN h.body->'hand_in'->'outputs' ELSE '[]'::jsonb END) decl(name)
 WHERE h.workspace_id=$1 AND h.card_id=$2 AND h.board_column=$3 AND h.round=$4 AND h.kind='hand_in' AND h.superseded_at IS NULL AND h.body->>'attempt'=r.attempt::text
 ), outputs AS (
 SELECT * FROM files
 UNION ALL
 SELECT NULL::uuid,'',d.name,d.run_id,'',d.created_at FROM declared d
 WHERE NOT EXISTS(SELECT 1 FROM files f WHERE f.key=d.name)
 AND (SELECT count(*) FROM files f WHERE f.name=d.name)<>1
 )
 SELECT id,key,name,run_id,blob_key FROM outputs WHERE ($5::uuid IS NULL OR id=$5) AND EXISTS(SELECT 1 FROM runs WHERE kind='role' AND role IN ('leader','agent'))
 ORDER BY created_at,run_id,id,name`, t.WS, g.CardID, g.Stage, g.Round, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (StudyRunOutput, error) {
		var o StudyRunOutput
		err := row.Scan(&o.ID, &o.Key, &o.Name, &o.RunID, &o.Blob)
		o.Available = o.Blob != ""
		return o, err
	})
}

type StudyActivity struct {
	Type  string    `json:"type"`
	At    time.Time `json:"at"`
	RunID uuid.UUID `json:"run_id"`
}

func (t *Tx) StudyRunActivity(ctx context.Context, g RunGroup) ([]StudyActivity, error) {
	rows, err := t.raw.Query(ctx, `SELECT e.type,e.created_at,r.id FROM agent_run r JOIN event e ON e.workspace_id=r.workspace_id AND e.card_id=r.card_id
 AND e.payload->>'agent_run_id'=r.id::text WHERE r.workspace_id=$1 AND r.card_id=$2 AND r.board_column=$3 AND r.round=$4 AND r.kind IN ('role','rerun','report')
 AND e.recipient_id IS NULL AND e.type IN ('agent_run.queued','agent_run.dispatched','agent_run.started','agent_run.completed','agent_run.failed','agent_run.cancelled','agent_run.requeued')
 ORDER BY e.id DESC LIMIT 50`, t.WS, g.CardID, g.Stage, g.Round)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (StudyActivity, error) {
		var e StudyActivity
		err := row.Scan(&e.Type, &e.At, &e.RunID)
		return e, err
	})
}
