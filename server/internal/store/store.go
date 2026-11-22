// Package store opens the transactions every request runs in: row-level
// security settings first, events written alongside the change, published
// only after commit.
package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	db "lykeion/server/pkg/db/generated"
)

type Actor struct {
	Type string // user | role | system
	ID   uuid.UUID
}

func User(id uuid.UUID) Actor { return Actor{Type: "user", ID: id} }

var System = Actor{Type: "system"}

type Store struct {
	LessonsEnabled bool // Server configuration, fixed before workers start.
	Pool           *pgxpool.Pool
	Publish        func(context.Context, []db.Event) // nil: no realtime
}

type Tx struct {
	LessonsEnabled bool
	maintenance    bool
	raw            pgx.Tx
	Q              *db.Queries
	WS             uuid.UUID
	events         []db.Event
	after          []func()
	afterPublish   []func()
}

// ComposerAfterUserStop uses event sequence numbers under the workspace
// execution lock, shared by cancellation and comment creation.
func (t *Tx) ComposerAfterUserStop(ctx context.Context, card, run uuid.UUID) (bool, error) {
	var found bool
	err := t.raw.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM event stop JOIN event message
		  ON message.workspace_id=stop.workspace_id AND message.card_id=stop.card_id
		 AND message.type='comment.created' AND message.id>stop.id
		JOIN comment c ON c.workspace_id=message.workspace_id AND c.card_id=message.card_id
		 AND c.id::text=message.payload->>'comment_id'
		WHERE stop.workspace_id=$1 AND stop.card_id=$2 AND stop.type='agent_run.cancelled'
		 AND stop.actor_type='user' AND stop.actor_id IS NOT NULL AND stop.payload->>'agent_run_id'=$3
		 AND c.reply_run_id IS NULL
		 AND NOT EXISTS (SELECT 1 FROM comment_cancellation x WHERE x.workspace_id=c.workspace_id AND x.comment_id=c.id)
		 AND NOT EXISTS (SELECT 1 FROM comment_reservation cr JOIN agent_run ar ON ar.id=cr.agent_run_id AND ar.workspace_id=cr.workspace_id WHERE cr.workspace_id=c.workspace_id AND cr.comment_id=c.id AND (cr.acknowledged_at IS NOT NULL OR ar.status IN ('queued','dispatched','running')))
		 AND NOT EXISTS (SELECT 1 FROM record r WHERE r.workspace_id=c.workspace_id AND r.card_id=c.card_id AND r.kind='comment_delivery' AND COALESCE(r.body->>'historical_unconfirmed','false') <> 'true' AND r.body->>'comment_id'=c.id::text)
	)`, t.WS, card, run.String()).Scan(&found)
	return found, err
}

// PendingBeforeUserStop identifies undelivered composer input superseded by a
// user stop without changing or acknowledging the original comments.
func (t *Tx) PendingBeforeUserStop(ctx context.Context, card uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := t.raw.Query(ctx, `WITH stop AS (
		SELECT max(id) AS id FROM event WHERE workspace_id=$1 AND card_id=$2
		 AND type='agent_run.cancelled' AND actor_type='user' AND actor_id IS NOT NULL
		 AND payload->>'role' IN ('leader', 'agent')
	) SELECT c.id FROM comment c CROSS JOIN stop
	 WHERE c.workspace_id=$1 AND c.card_id=$2 AND c.reply_run_id IS NULL AND stop.id IS NOT NULL
	 AND NOT EXISTS (SELECT 1 FROM event e WHERE e.workspace_id=c.workspace_id AND e.card_id=c.card_id AND e.type='comment.created' AND e.payload->>'comment_id'=c.id::text AND e.id>stop.id)
	 AND NOT EXISTS (SELECT 1 FROM comment_cancellation x WHERE x.workspace_id=c.workspace_id AND x.comment_id=c.id)
	 AND NOT EXISTS (SELECT 1 FROM comment_reservation cr JOIN agent_run ar ON ar.id=cr.agent_run_id AND ar.workspace_id=cr.workspace_id WHERE cr.workspace_id=c.workspace_id AND cr.comment_id=c.id AND (cr.acknowledged_at IS NOT NULL OR ar.status IN ('queued','dispatched','running')))
	 AND NOT EXISTS (SELECT 1 FROM record r WHERE r.workspace_id=c.workspace_id AND r.card_id=c.card_id AND r.kind='comment_delivery' AND COALESCE(r.body->>'historical_unconfirmed','false') <> 'true' AND r.body->>'comment_id'=c.id::text)`, t.WS, card)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make(map[uuid.UUID]bool)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

type AgentCapabilities struct {
	Skills     json.RawMessage `json:"skills"`
	Connectors json.RawMessage `json:"connectors"`
}

func (t *Tx) AgentCapabilities(ctx context.Context, agent uuid.UUID) (AgentCapabilities, error) {
	var c AgentCapabilities
	err := t.raw.QueryRow(ctx, `SELECT skills, connectors FROM agent_capabilities WHERE workspace_id=$1 AND agent_id=$2`, t.WS, agent).Scan(&c.Skills, &c.Connectors)
	return c, err
}

func (t *Tx) SetAgentCapabilities(ctx context.Context, agent uuid.UUID, c AgentCapabilities) error {
	_, err := t.raw.Exec(ctx, `INSERT INTO agent_capabilities(workspace_id,agent_id,skills,connectors) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,agent_id) DO UPDATE SET skills=excluded.skills,connectors=excluded.connectors`, t.WS, agent, c.Skills, c.Connectors)
	return err
}

func (t *Tx) AgentHasLiveStudyRuns(ctx context.Context, agent uuid.UUID) (bool, error) {
	var live bool
	err := t.raw.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_run WHERE workspace_id=$1 AND agent_id=$2 AND kind <> 'coresearch' AND status IN ('queued','dispatched','running'))`, t.WS, agent).Scan(&live)
	return live, err
}

type AgentStudyWork struct {
	CardID     uuid.UUID `json:"card_id"`
	ActiveRole string    `json:"active_role"`
}

func (t *Tx) AgentStudyWork(ctx context.Context, agent uuid.UUID) ([]AgentStudyWork, error) {
	rows, err := t.raw.Query(ctx, `SELECT DISTINCT ON (card_id) card_id,
  CASE WHEN status IN ('queued','dispatched','running') THEN role ELSE '' END AS active_role
FROM agent_run
WHERE workspace_id=$1 AND agent_id=$2 AND kind='role' AND card_id IS NOT NULL
ORDER BY card_id, (status IN ('queued','dispatched','running')) DESC, created_at DESC, id DESC`, t.WS, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	work := []AgentStudyWork{}
	for rows.Next() {
		var entry AgentStudyWork
		if err := rows.Scan(&entry.CardID, &entry.ActiveRole); err != nil {
			return nil, err
		}
		work = append(work, entry)
	}
	return work, rows.Err()
}

func (t *Tx) AgentTeams(ctx context.Context, agent uuid.UUID) ([]db.TeamAgent, error) {
	rows, err := t.raw.Query(ctx, `SELECT workspace_id, pack_id, user_id, member, agent_id FROM team_agent WHERE workspace_id=$1 AND agent_id=$2 ORDER BY pack_id, member`, t.WS, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []db.TeamAgent
	for rows.Next() {
		var item db.TeamAgent
		if err := rows.Scan(&item.WorkspaceID, &item.PackID, &item.UserID, &item.Member, &item.AgentID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// SetCoresearchRunConnectors binds the selected workspace connectors to one run.
func (t *Tx) SetCoresearchRunConnectors(ctx context.Context, run uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if _, err := t.raw.Exec(ctx, `INSERT INTO coresearch_run_connector(workspace_id, agent_run_id, connector_id) VALUES ($1,$2,$3)`, t.WS, run, id); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tx) CoresearchRunConnectors(ctx context.Context, run uuid.UUID) ([]uuid.UUID, error) {
	rows, err := t.raw.Query(ctx, `SELECT connector_id FROM coresearch_run_connector WHERE workspace_id=$1 AND agent_run_id=$2 ORDER BY connector_id`, t.WS, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CardTeamAssignment describes one Team assigned to a study stage.
type CardTeamAssignment struct {
	CardID uuid.UUID
	Column string
	PackID uuid.UUID
	Name   string
	Stage  string
}

// CardTeamAssignments loads all stage assignments in one workspace-scoped query.
func (t *Tx) CardTeamAssignments(ctx context.Context) ([]CardTeamAssignment, error) {
	rows, err := t.raw.Query(ctx, `SELECT ct.card_id, ct.board_column, ct.pack_id, p.name, p.stage
 FROM card_team ct JOIN field_pack p ON p.workspace_id = ct.workspace_id AND p.id = ct.pack_id
 WHERE ct.workspace_id = $1`, t.WS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var assignments []CardTeamAssignment
	for rows.Next() {
		var a CardTeamAssignment
		if err := rows.Scan(&a.CardID, &a.Column, &a.PackID, &a.Name, &a.Stage); err != nil {
			return nil, err
		}
		assignments = append(assignments, a)
	}
	return assignments, rows.Err()
}

type CardRunningCLI struct {
	CardID  uuid.UUID `json:"-"`
	Column  string    `json:"board_column"`
	Harness string    `json:"harness"`
}

func (t *Tx) CardRunningCLIs(ctx context.Context) ([]CardRunningCLI, error) {
	rows, err := t.raw.Query(ctx, `SELECT card_id, board_column, harness FROM agent_run
 WHERE workspace_id = $1 AND card_id IS NOT NULL AND status = 'running' ORDER BY created_at, id`, t.WS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CardRunningCLI
	for rows.Next() {
		var item CardRunningCLI
		if err := rows.Scan(&item.CardID, &item.Column, &item.Harness); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// AfterCommit runs fn once the transaction has committed, and never when it
// rolls back. Side effects outside the database, such as deleting blobs,
// belong here.
func (t *Tx) AfterCommit(fn func()) { t.after = append(t.after, fn) }

// AfterPublish runs after this transaction's events have been published.
func (t *Tx) AfterPublish(fn func()) { t.afterPublish = append(t.afterPublish, fn) }

// Savepoint runs fn inside a savepoint of t: an error from fn undoes only
// what fn wrote and leaves t usable. Events fn emits are published with t's
// when fn succeeds; what fn leaves to AfterCommit runs once t commits, unless
// fn's work was undone.
func (t *Tx) Savepoint(ctx context.Context, fn func(*Tx) error) error {
	sp, err := t.raw.Begin(ctx)
	if err != nil {
		return err
	}
	inner := &Tx{Q: db.New(sp), WS: t.WS, raw: sp, maintenance: t.maintenance, LessonsEnabled: t.LessonsEnabled}
	if err := fn(inner); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return err
	}
	t.after = append(t.after, inner.after...)
	t.events = append(t.events, inner.events...)
	t.afterPublish = append(t.afterPublish, inner.afterPublish...)
	return nil
}

// Emit records an event in the same transaction as the change it describes.
// Inserts are serialised per workspace, so event ids commit in id order and a
// reader resuming after id N never skips a lower id that committed later.
func (t *Tx) Emit(ctx context.Context, typ string, card *uuid.UUID, actor Actor, payload any) error {
	return t.emit(ctx, nil, typ, card, actor, payload)
}

// EmitPrivate records an event for one person without adding study activity.
func (t *Tx) EmitPrivate(ctx context.Context, recipient uuid.UUID, typ string, actor Actor, payload any) error {
	if recipient == uuid.Nil {
		return errors.New("private event requires a recipient")
	}
	return t.emit(ctx, &recipient, typ, nil, actor, payload)
}

func (t *Tx) emit(ctx context.Context, recipient *uuid.UUID, typ string, card *uuid.UUID, actor Actor, payload any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := t.Q.LockWorkspaceEvents(ctx, t.WS); err != nil {
		return err
	}
	var actorID *uuid.UUID
	if actor.ID != uuid.Nil {
		actorID = &actor.ID
	}
	e, err := t.Q.InsertEvent(ctx, db.InsertEventParams{
		WorkspaceID: t.WS, RecipientID: recipient, CardID: card, Type: typ, ActorType: actor.Type, ActorID: actorID, Payload: b,
	})
	if err != nil {
		return err
	}
	t.events = append(t.events, e)
	return nil
}

func (s *Store) InWorkspace(ctx context.Context, ws, user uuid.UUID, fn func(*Tx) error) error {
	return s.run(ctx, ws, map[string]string{"app.workspace_id": ws.String(), "app.user_id": user.String()}, fn)
}

// InWorkspaceOn runs one tenant transaction on a session reserved by the caller.
func (s *Store) InWorkspaceOn(ctx context.Context, conn *pgxpool.Conn, ws, user uuid.UUID, fn func(*Tx) error) error {
	return s.runWith(ctx, conn, ws, map[string]string{"app.workspace_id": ws.String(), "app.user_id": user.String()}, pgx.TxOptions{}, fn)
}

// InWorkspaceReadCommitted gives cleanup a fresh snapshot after acquiring locks.
func (s *Store) InWorkspaceReadCommitted(ctx context.Context, ws, user uuid.UUID, fn func(*Tx) error) error {
	return s.runWith(ctx, s.Pool, ws, map[string]string{"app.workspace_id": ws.String(), "app.user_id": user.String()}, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
}

// InWorkspaceSnapshot is used for hand-ins that validate several related facts.
// Concurrent changes may serialize before or after the hand-in, never between
// its checks. A rolled-back attempt publishes no events.
func (s *Store) InWorkspaceSnapshot(ctx context.Context, ws, user uuid.UUID, fn func(*Tx) error) error {
	for attempt := 1; ; attempt++ {
		err := s.runWith(ctx, s.Pool, ws, map[string]string{"app.workspace_id": ws.String(), "app.user_id": user.String()}, pgx.TxOptions{IsoLevel: pgx.Serializable}, fn)
		var e *pgconn.PgError
		if attempt >= 4 || !errors.As(err, &e) || e.Code != "40001" {
			return err
		}
	}
}

// AsUser runs without a workspace: only rows visible to the user across
// workspaces (their memberships, and invitations sent to their email).
func (s *Store) AsUser(ctx context.Context, user uuid.UUID, email string, fn func(*Tx) error) error {
	return s.run(ctx, uuid.Nil, map[string]string{"app.user_id": user.String(), "app.user_email": email}, fn)
}

// Global is for the identity tables, which have no workspace.
func (s *Store) Global() *db.Queries { return db.New(s.Pool) }

func (s *Store) run(ctx context.Context, ws uuid.UUID, settings map[string]string, fn func(*Tx) error) error {
	return s.runWith(ctx, s.Pool, ws, settings, pgx.TxOptions{}, fn)
}

type txStarter interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func (s *Store) runWith(ctx context.Context, source txStarter, ws uuid.UUID, settings map[string]string, opts pgx.TxOptions, fn func(*Tx) error) error {
	tx, err := source.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for k, v := range settings {
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", k, v); err != nil {
			return err
		}
	}
	t := &Tx{Q: db.New(tx), WS: ws, raw: tx, LessonsEnabled: s.LessonsEnabled}
	if err := fn(t); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, fn := range t.after {
		fn()
	}
	if s.Publish != nil && len(t.events) > 0 {
		s.Publish(ctx, t.events)
	}
	for _, fn := range t.afterPublish {
		fn()
	}
	return nil
}

// Maintenance runs narrowly scoped global maintenance helpers and all resulting
// tenant mutations in one commit. Call Workspace before accessing tenant rows.
func (s *Store) Maintenance(ctx context.Context, fn func(*Tx) error) error {
	return s.run(ctx, uuid.Nil, map[string]string{"app.workspace_id": uuid.Nil.String(), "app.user_id": uuid.Nil.String()}, func(tx *Tx) error { tx.maintenance = true; return fn(tx) })
}

// Workspace changes tenant scope inside a maintenance transaction. It preserves
// the transaction and its accumulated events for publication only after commit.
func (t *Tx) Workspace(ctx context.Context, ws uuid.UUID) error {
	if _, err := t.raw.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", ws.String()); err != nil {
		return err
	}
	t.WS = ws
	return nil
}

// IsMaintenance identifies internal maintenance transactions; it is never set by request-scoped entrypoints.
func (t *Tx) IsMaintenance() bool { return t != nil && t.maintenance }
