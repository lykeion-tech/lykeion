package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func TestWorkspaceMCPClaimRefusesCatalogNameCollision(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	_, err := f.Env.Owner.Exec(ctx, `INSERT INTO workspace_mcp_server(workspace_id,id,name,transport,config_ciphertext,created_by) VALUES ($1,$2,'uniprot','url',$3,$4)`, f.WS, uuid.New(), bytes.Repeat([]byte{1}, 64), f.Owner)
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "collision", "claude")
	claims, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(claims) != 0 {
		t.Fatalf("ambiguous claim: %+v %v", claims, err)
	}
	var status, why string
	var tokens, settlements int
	if err := f.Env.Owner.QueryRow(ctx, `SELECT status,error FROM agent_run LIMIT 1`).Scan(&status, &why); err != nil {
		t.Fatal(err)
	}
	f.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run_token`).Scan(&tokens)
	f.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run_settlement`).Scan(&settlements)
	if status != "failed" || why == "" || tokens != 0 || settlements != 1 || f.settledCount() != 1 {
		t.Fatalf("refusal status=%s error=%s tokens=%d settlements=%d callbacks=%d", status, why, tokens, settlements, f.settledCount())
	}
}

func TestWorkspaceMCPTeamRefsAndCommandApproval(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	remote, command, undeclared := uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{remote, command, undeclared} {
		transport := "url"
		if i == 1 {
			transport = "command"
		}
		_, err := f.Env.Owner.Exec(ctx, `INSERT INTO workspace_mcp_server(workspace_id,id,name,transport,config_ciphertext,created_by) VALUES ($1,$2,$3,$4,$5,$6)`, f.WS, id, "connector-"+id.String(), transport, bytes.Repeat([]byte{1}, 64), f.Owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	zip := testutil.Pack(t, testutil.PackOpts{Connectors: map[string][]map[string]any{"member-1": {{"name": "remote-alias", "workspace": remote.String()}, {"name": "command-alias", "workspace": command.String()}}}})
	p, err := pack.Parse(zip)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(p.Manifest)
	var packID uuid.UUID
	if err := f.Env.Owner.QueryRow(ctx, `INSERT INTO field_pack(workspace_id,name,version,sha256,manifest,blob_key,source,domain,stage) VALUES ($1,'ref-team','1','sha',$2,'blob','shipped','test','grounding') RETURNING id`, f.WS, manifest).Scan(&packID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Env.Owner.Exec(ctx, `INSERT INTO card_team(workspace_id,card_id,board_column,pack_id) VALUES ($1,$2,'grounding',$3)`, f.WS, f.Card, packID)
	if err != nil {
		t.Fatal(err)
	}
	read := func() []WorkspaceConnectorRow {
		var rows []WorkspaceConnectorRow
		err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
			var e error
			rows, e = WorkspaceConnectorRows(ctx, tx, db.AgentRun{CardID: &f.Card, Role: "member-1", BoardColumn: "grounding", ResponsibleUserID: f.Owner})
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := read()
	if len(rows) != 1 || rows[0].ID != remote || rows[0].DoorName != "remote-alias" {
		t.Fatalf("unapproved refs: %+v", rows)
	}
	var agentID uuid.UUID
	if err := f.Env.Owner.QueryRow(ctx, `INSERT INTO agent(workspace_id,name,harness,owner_id) VALUES ($1,'member','claude',$2) RETURNING id`, f.WS, f.Owner).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Env.Owner.Exec(ctx, `INSERT INTO team_agent(workspace_id,pack_id,user_id,member,agent_id) VALUES ($1,$2,$3,'member-1',$4)`, f.WS, packID, f.Owner, agentID)
	if err != nil {
		t.Fatal(err)
	}
	rows = read()
	if len(rows) != 2 || rows[1].ID != command {
		t.Fatalf("approved refs: %+v", rows)
	}
	for _, r := range rows {
		if r.ID == undeclared {
			t.Fatal("undeclared connector escaped Team")
		}
	}
}

func TestWorkspaceMCPAttemptCleanupOnlyAfterCommit(t *testing.T) {
	for _, sweep := range []bool{false, true} {
		name := "report"
		if sweep {
			name = "sweep"
		}
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			f.enqueue(t, 1, "claude")
			ref, id := f.claimStart(t, f.runtime(t, f.Owner, "attempt", "claude"))
			ended := 0
			f.T.OnAttemptEnded = func(ws, run uuid.UUID, attempt int32) {
				if ws != f.WS || run != id || attempt != 1 {
					t.Errorf("wrong cleanup authority: %s %s %d", ws, run, attempt)
				}
				var status string
				if err := f.Env.Owner.QueryRow(ctx, `SELECT status FROM agent_run WHERE id=$1`, id).Scan(&status); err != nil || status != "queued" {
					t.Errorf("cleanup before commit: %s %v", status, err)
				}
				ended++
			}
			if _, err := f.Env.Owner.Exec(ctx, `CREATE FUNCTION reject_retry_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type IN ('agent_run.queued','agent_run.requeued') THEN RAISE EXCEPTION 'injected event failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_retry_event BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION reject_retry_event()`); err != nil {
				t.Fatal(err)
			}
			if sweep {
				if _, err := f.Env.Owner.Exec(ctx, `UPDATE runtime SET last_seen_at=now()-interval '5 minutes' WHERE id=$1`, ref.RuntimeID); err != nil {
					t.Fatal(err)
				}
			}
			end := func() error {
				if sweep {
					return f.T.Sweep(ctx)
				}
				_, err := f.T.Fail(ctx, f.Owner, ref, id, Report{FailureReason: "provider_network", Error: "retry"})
				return err
			}
			if err := end(); err == nil {
				t.Fatal("injected rollback did not fail")
			}
			if ended != 0 {
				t.Fatal("rollback closed active attempt")
			}
			if _, err := f.Env.Owner.Exec(ctx, `DROP TRIGGER reject_retry_event ON event`); err != nil {
				t.Fatal(err)
			}
			if err := end(); err != nil {
				t.Fatal(err)
			}
			if ended != 1 || f.settledCount() != 0 {
				t.Fatalf("cleanup=%d scientific settlements=%d", ended, f.settledCount())
			}
			if err := end(); err != nil {
				t.Fatal(err)
			}
			if ended != 1 {
				t.Fatal("replay closed a newer attempt")
			}
		})
	}
}
