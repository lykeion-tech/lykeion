package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AgentRef is a column holding an agent id. Owned rows belong to the agent
// itself and go with it (its foreign key cascades); the others are history
// that outlives it.
type AgentRef struct {
	Table, Column string
	Owned         bool
}

// AgentRefs lists every foreign key to agent(id), plus the assignee columns,
// which name an agent without a foreign key.
func (t *Tx) AgentRefs(ctx context.Context) ([]AgentRef, error) {
	rows, err := t.raw.Query(ctx, `SELECT c.conrelid::regclass::text, a.attname, c.confdeltype = 'c'
FROM pg_constraint c CROSS JOIN LATERAL unnest(c.conkey, c.confkey) k(col, ref)
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.col
JOIN pg_attribute r ON r.attrelid = c.confrelid AND r.attnum = k.ref
WHERE c.contype = 'f' AND c.confrelid = 'public.agent'::regclass AND r.attname = 'id'
ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AgentRef, error) {
		var ref AgentRef
		return ref, r.Scan(&ref.Table, &ref.Column, &ref.Owned)
	})
	return append(out, AgentRef{Table: "card", Column: "assignee_id"}, AgentRef{Table: "autoresearch", Column: "assignee_id"}), err
}

// MoveAgentRefs points the ref's rows that name any of from at to, or, for
// owned rows, deletes them.
func (t *Tx) MoveAgentRefs(ctx context.Context, ref AgentRef, from []uuid.UUID, to uuid.UUID) (int64, error) {
	// Table names come from regclass, which quotes them already.
	col := pgx.Identifier{ref.Column}.Sanitize()
	where := ` WHERE workspace_id = $1 AND ` + col + ` = ANY($2)`
	if ref.Table == "card" || ref.Table == "autoresearch" {
		where += ` AND assignee_type IN ('agent', 'team')`
	}
	sql, args := `DELETE FROM `+ref.Table+where, []any{t.WS, from}
	if !ref.Owned {
		sql, args = `UPDATE `+ref.Table+` SET `+col+` = $3`+where, append(args, to)
	}
	tag, err := t.raw.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}
