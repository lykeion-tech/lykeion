package store

import (
	"context"

	"github.com/google/uuid"
	db "lykeion/server/pkg/db/generated"
)

// ShippedSoloTeam finds the shipped stage Team led by this agent.
func (t *Tx) ShippedSoloTeam(ctx context.Context, agent uuid.UUID, stage string) (db.FieldPack, error) {
	var id uuid.UUID
	err := t.raw.QueryRow(ctx, `SELECT p.id FROM team_agent ta JOIN field_pack p ON p.workspace_id=ta.workspace_id AND p.id=ta.pack_id
 WHERE ta.workspace_id=$1 AND ta.agent_id=$2 AND p.source='shipped' AND p.stage=$3
 AND p.name IN ('Grounding Team','Hypotheses Team','Experimentation Team','Validation Team')
 AND ta.member=p.manifest->'leader'->>'name' LIMIT 1`, t.WS, agent, stage).Scan(&id)
	if err != nil {
		return db.FieldPack{}, err
	}
	return t.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: t.WS, ID: id})
}
