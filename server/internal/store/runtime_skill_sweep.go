package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// RuntimeSkillTimeout identifies the existing event fields for one timeout.
type RuntimeSkillTimeout struct {
	ID        uuid.UUID
	RuntimeID uuid.UUID
	OwnerID   uuid.UUID
	Operation string
}

type RuntimeSkillWorkspace struct {
	ID       uuid.UUID
	Eligible bool
}

// RuntimeSkillTimeoutWorkspaces pages workspace IDs and their eligibility for maintenance.
func (t *Tx) RuntimeSkillTimeoutWorkspaces(ctx context.Context, after uuid.UUID) ([]RuntimeSkillWorkspace, error) {
	if !t.maintenance || t.WS != uuid.Nil {
		return nil, errors.New("runtime skill timeout discovery requires maintenance scope")
	}
	rows, err := t.raw.Query(ctx, `SELECT * FROM public.runtime_skill_timeout_candidates($1::uuid)`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []RuntimeSkillWorkspace
	for rows.Next() {
		var id RuntimeSkillWorkspace
		if err := rows.Scan(&id.ID, &id.Eligible); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// TimeoutRuntimeSkills updates only the bounded, still-expired tenant rows.
func (t *Tx) TimeoutRuntimeSkills(ctx context.Context, limit int) ([]RuntimeSkillTimeout, error) {
	if !t.maintenance || t.WS == uuid.Nil || limit < 1 || limit > 64 {
		return nil, errors.New("invalid runtime skill timeout maintenance scope")
	}
	rows, err := t.raw.Query(ctx, `WITH expired AS (
		SELECT id FROM public.runtime_skill_request
		WHERE workspace_id=$1 AND status IN ('pending','running') AND expires_at<=pg_catalog.statement_timestamp()
		ORDER BY expires_at,id LIMIT $2 FOR UPDATE
	) UPDATE public.runtime_skill_request q
	SET status='timeout', completed_at=pg_catalog.clock_timestamp()
	FROM expired e
	WHERE q.workspace_id=$1 AND q.id=e.id AND q.status IN ('pending','running') AND q.expires_at<=pg_catalog.statement_timestamp()
	RETURNING q.id,q.runtime_id,q.owner_id,q.operation`, t.WS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RuntimeSkillTimeout
	for rows.Next() {
		var item RuntimeSkillTimeout
		if err := rows.Scan(&item.ID, &item.RuntimeID, &item.OwnerID, &item.Operation); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
