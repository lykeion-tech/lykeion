package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestStandaloneAgentCapabilitiesReachClaim(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var id uuid.UUID
	if err := f.Env.Owner.QueryRow(ctx, `INSERT INTO agent(workspace_id,name,harness,owner_id) VALUES($1,'custom','claude',$2) RETURNING id`, f.WS, f.Owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(ctx, `INSERT INTO agent_capabilities(workspace_id,agent_id,skills,connectors) VALUES($1,$2,'["aeon"]','[{"name":"ensembl","catalogue":"ensembl"}]')`, f.WS, id); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	if _, err := f.Env.Owner.Exec(ctx, `UPDATE agent_run SET agent_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ref := f.runtime(t, f.Owner, "customize-runtime", "claude")
	claims, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %+v %v", claims, err)
	}
	if len(claims[0].Skills) != 1 || claims[0].Skills[0].Name != "aeon" {
		t.Fatalf("skills: %+v", claims[0].Skills)
	}
	if len(claims[0].Connectors) != 1 || claims[0].Connectors[0].Catalogue != "ensembl" {
		t.Fatalf("connectors: %+v", claims[0].Connectors)
	}
}

func TestStandaloneWorkspaceConnectorSelection(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var agent uuid.UUID
	if err := f.Env.Owner.QueryRow(ctx, `INSERT INTO agent(workspace_id,name,harness,owner_id) VALUES($1,'custom','claude',$2) RETURNING id`, f.WS, f.Owner).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	selected, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{selected, other} {
		if _, err := f.Env.Owner.Exec(ctx, `INSERT INTO workspace_mcp_server(workspace_id,id,name,transport,config_ciphertext,created_by) VALUES($1,$2,$3,'url',$4,$5)`, f.WS, id, "connector-"+id.String(), bytes.Repeat([]byte{1}, 64), f.Owner); err != nil {
			t.Fatal(err)
		}
	}
	config := `[{"name":"chosen","workspace":"` + selected.String() + `"}]`
	if _, err := f.Env.Owner.Exec(ctx, `INSERT INTO agent_capabilities(workspace_id,agent_id,skills,connectors) VALUES($1,$2,'[]',$3::jsonb)`, f.WS, agent, config); err != nil {
		t.Fatal(err)
	}
	var rows []WorkspaceConnectorRow
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		var err error
		rows, err = WorkspaceConnectorRows(ctx, tx, db.AgentRun{AgentID: &agent, ResponsibleUserID: f.Owner})
		return err
	})
	if err != nil || len(rows) != 1 || rows[0].ID != selected || rows[0].DoorName != "chosen" {
		t.Fatalf("workspace connector selection: %+v %v", rows, err)
	}
}
