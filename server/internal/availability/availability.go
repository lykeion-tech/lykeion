// Package availability manages workspace policy independently of immutable content and credentials.
package availability

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/science"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"strings"
)

type Policy struct {
	Enabled  bool  `json:"policy_enabled"`
	Revision int64 `json:"policy_revision"`
}

var ErrForbidden = errors.New("only workspace owners and admins may change availability")
var ErrInvalid = errors.New("invalid availability policy")

type Stale struct{ Revision int64 }

func (*Stale) Error() string { return "availability changed; try again with the current policy" }
func Get(ctx context.Context, tx *store.Tx, kind, id string) (Policy, error) {
	if !(kind == "skill" && strings.HasPrefix(id, "bundle:")) {
		uid, err := uuid.Parse(id)
		if err != nil {
			return Policy{}, pgx.ErrNoRows
		}
		id = uid.String()
	}
	row, err := tx.Q.GetWorkspaceAvailability(ctx, db.GetWorkspaceAvailabilityParams{WorkspaceID: tx.WS, Kind: kind, ResourceID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{Enabled: true}, nil
	}
	return Policy{true, row.Revision}, err
}
func Set(ctx context.Context, st *store.Store, ws, user uuid.UUID, kind, id string, expected int64, enabled bool) (Policy, error) {
	var out Policy
	if expected < 0 || (kind != "skill" && kind != "mcp") {
		return out, ErrInvalid
	}
	err := st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(ctx, ws); err != nil {
			return err
		}
		m, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		if m.Role != "owner" && m.Role != "admin" {
			return ErrForbidden
		}
		if kind == "skill" && strings.HasPrefix(id, "bundle:") {
			if !science.Has(strings.TrimPrefix(id, "bundle:")) {
				return pgx.ErrNoRows
			}
		} else {
			uid, err := uuid.Parse(id)
			if err != nil {
				return pgx.ErrNoRows
			}
			id = uid.String()
			if kind == "skill" {
				_, err = tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: uid})
			} else {
				_, err = tx.Q.LockWorkspaceMCPServer(ctx, db.LockWorkspaceMCPServerParams{WorkspaceID: ws, ID: uid})
			}
			if err != nil {
				return err
			}
		}
		current, err := Get(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		if current.Revision != expected {
			return &Stale{current.Revision}
		}
		row, err := tx.Q.SetWorkspaceAvailability(ctx, db.SetWorkspaceAvailabilityParams{WorkspaceID: ws, Kind: kind, ResourceID: id, Enabled: enabled, UpdatedBy: user})
		if err != nil {
			return err
		}
		out = Policy{row.Enabled, row.Revision}
		event := "skill.updated"
		if kind == "mcp" {
			event = "connector.updated"
		}
		payload := map[string]any{"policy_enabled": enabled, "policy_revision": row.Revision}
		if kind == "skill" {
			payload["skill_id"] = id
		} else {
			payload["connector_id"] = id
			payload["origin"] = "custom"
		}
		return tx.Emit(ctx, event, nil, store.User(user), payload)
	})
	return out, err
}
