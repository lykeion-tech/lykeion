package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// A private copy's runs keep the readers they had once the copy is merged
// into the shared laboratory agent.
func TestMergedRunsKeepTheirReaders(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	ctx := t.Context()
	owner, wsID := uuid.MustParse(s.userID(t, a)), uuid.MustParse(ws)
	if _, err := s.H.AgentRuns.Register(ctx, owner, wsID, "d-a", "d-a", []string{"claude"}, nil, agent.Platform{OS: "linux"}); err != nil {
		t.Fatal(err)
	}
	var copyID uuid.UUID
	if err := s.H.Store.InWorkspace(ctx, wsID, owner, func(tx *store.Tx) error {
		if err := pack.InstallShipped(ctx, tx, s.H.Blobs, pack.Shipped()); err != nil {
			return err
		}
		pk, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: wsID, Stage: board.Grounding})
		if err != nil {
			return err
		}
		var m pack.Manifest
		if err := json.Unmarshal(pk.Manifest, &m); err != nil {
			return err
		}
		rows, err := team.Add(ctx, tx, pk, m, owner)
		for _, r := range rows {
			if r.Member == m.Leader.Name && r.UserID != nil {
				copyID = r.AgentID
			}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if copyID == uuid.Nil {
		t.Fatal("no private leader copy")
	}
	// A private Coresearch turn and a study run, both worked by the copy.
	chat, _ := csChat(t, s, a, ws, p.ID)
	private := csTurn(t, s, a, ws, chat)
	c := s.idleCard(t, a, ws, "q")
	study := uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE agent_run SET agent_id = $2 WHERE id = $1`, private.ID, copyID)
	exec(`INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, skill, max_attempts, agent_id)
VALUES ($1, $2, $3, $4, 'grounding', 1, 'leader', 'claude', '', '', 1, $5)`, study, wsID, uuid.MustParse(c.ID), owner, copyID)

	reads := func() [3]int {
		return [3]int{
			s.call(t, "GET", "/api/agent-runs/"+private.ID.String(), b, ws, nil, nil),
			s.call(t, "GET", chat+"/messages", b, ws, nil, nil),
			s.call(t, "GET", "/api/agent-runs/"+study.String(), b, ws, nil, nil),
		}
	}
	before := reads()
	if before != [3]int{404, 404, 200} {
		t.Fatalf("before the merge b reads %v", before)
	}
	if err := s.H.Store.InWorkspace(ctx, wsID, uuid.Nil, func(tx *store.Tx) error { return team.InstallLaboratory(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	var moved int
	if err := s.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run r JOIN agent g ON g.id = r.agent_id
WHERE r.id = ANY($1) AND g.laboratory = 'built-in' AND g.id = $2`, []uuid.UUID{private.ID, study}, copyID).Scan(&moved); err != nil || moved != 2 {
		t.Fatalf("%d runs on the shared agent: %v", moved, err)
	}
	if after := reads(); after != before {
		t.Fatalf("after the merge b reads %v, before %v", after, before)
	}
}
