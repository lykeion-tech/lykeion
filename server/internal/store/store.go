// Package store opens the transactions every request runs in: row-level
// security settings first, events written alongside the change, published
// only after commit.
package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	Pool    *pgxpool.Pool
	Publish func(context.Context, []db.Event) // nil: no realtime
}

type Tx struct {
	maintenance  bool
	raw          pgx.Tx
	Q            *db.Queries
	WS           uuid.UUID
	events       []db.Event
	after        []func()
	afterPublish []func()
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
	inner := &Tx{Q: db.New(sp), WS: t.WS, raw: sp, maintenance: t.maintenance}
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
		WorkspaceID: t.WS, CardID: card, Type: typ, ActorType: actor.Type, ActorID: actorID, Payload: b,
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

// AsUser runs without a workspace: only rows visible to the user across
// workspaces (their memberships, and invitations sent to their email).
func (s *Store) AsUser(ctx context.Context, user uuid.UUID, email string, fn func(*Tx) error) error {
	return s.run(ctx, uuid.Nil, map[string]string{"app.user_id": user.String(), "app.user_email": email}, fn)
}

// Global is for the identity tables, which have no workspace.
func (s *Store) Global() *db.Queries { return db.New(s.Pool) }

func (s *Store) run(ctx context.Context, ws uuid.UUID, settings map[string]string, fn func(*Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for k, v := range settings {
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", k, v); err != nil {
			return err
		}
	}
	t := &Tx{Q: db.New(tx), WS: ws, raw: tx}
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
