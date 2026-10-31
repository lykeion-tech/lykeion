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
	raw    pgx.Tx
	Q      *db.Queries
	WS     uuid.UUID
	events []db.Event
	after  []func()
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

// Savepoint runs fn inside a savepoint of t: an error from fn undoes only
// what fn wrote and leaves t usable. Events fn emits are dropped; what fn
// leaves to AfterCommit runs once t commits, unless fn's work was undone.
func (t *Tx) Savepoint(ctx context.Context, fn func(*Tx) error) error {
	sp, err := t.raw.Begin(ctx)
	if err != nil {
		return err
	}
	inner := &Tx{Q: db.New(sp), WS: t.WS, raw: sp}
	if err := fn(inner); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return err
	}
	t.after = append(t.after, inner.after...)
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
	return nil
}

// Maintenance runs narrowly scoped global maintenance helpers and all resulting
// tenant mutations in one commit. Call Workspace before accessing tenant rows.
func (s *Store) Maintenance(ctx context.Context, fn func(*Tx) error) error {
	return s.run(ctx, uuid.Nil, map[string]string{"app.workspace_id": uuid.Nil.String(), "app.user_id": uuid.Nil.String()}, fn)
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
