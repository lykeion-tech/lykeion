package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestSoloShippedLeaderRoutesMemberSkill(t *testing.T) {
	l := newAgentLab(t)
	ctx := context.Background()
	j, _ := noulJev(t, 0.9)
	l.s.H.Engine.Jev = j
	zip, p, err := pack.ReadShipped(pack.Shipped(), "Grounding Team")
	if err != nil {
		t.Fatal(err)
	}
	ws := uuid.MustParse(l.ws)
	key, err := pack.PutBlob(ctx, l.s.H.Blobs, ws, zip, p.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	var a uuid.UUID
	if err = l.s.Env.Owner.QueryRow(ctx, `SELECT assignee_id FROM card WHERE id=$1`, l.card.ID).Scan(&a); err != nil {
		t.Fatal(err)
	}
	err = l.s.H.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		row, e := pack.Insert(ctx, tx, key, p, nil)
		if e != nil {
			return e
		}
		return tx.Q.InsertTeamAgent(ctx, db.InsertTeamAgentParams{WorkspaceID: ws, PackID: row.ID, AgentID: a, Member: p.Manifest.Leader.Name})
	})
	if err != nil {
		t.Fatal(err)
	}
	folder := routedFolder(t, l, l.claim(t).token, "literature-search")
	if len(folderFile(t, folder, "SKILL.md")) == 0 {
		t.Fatal("empty skill")
	}
}
