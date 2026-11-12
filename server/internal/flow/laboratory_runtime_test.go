package flow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// A laboratory agent's run takes its CLI from the runtime it is bound to and
// the model the shipped manifest gives that CLI, whatever CLI the study chose.
func TestLabRunUsesBoundRuntimeCLI(t *testing.T) {
	ctx := context.Background()
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	runs := &service.AgentRuns{Store: st}
	e := &Engine{Store: st, AgentRuns: runs, Inline: true}
	ws := uuid.New()
	u, err := st.Global().UpsertUserByEmail(ctx, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	in := func(user uuid.UUID, fn func(tx *store.Tx) error) {
		t.Helper()
		if err := st.InWorkspace(ctx, ws, user, fn); err != nil {
			t.Fatal(err)
		}
	}
	in(u.ID, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: ws, Slug: "lab", Name: "Lab"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws, UserID: u.ID, Role: "owner"}); err != nil {
			return err
		}
		return pack.InstallShipped(ctx, tx, storage.Local{Dir: t.TempDir()}, pack.Shipped())
	})
	in(uuid.Nil, func(tx *store.Tx) error { return team.InstallLaboratory(ctx, tx) })
	if _, err := runs.Register(ctx, u.ID, ws, "codex-box", "box", []string{"codex"}, nil, agent.Platform{OS: "linux"}); err != nil {
		t.Fatal(err)
	}
	var c db.Card
	var m pack.Manifest
	in(u.ID, func(tx *store.Tx) error {
		p, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: ws, Stage: board.Grounding})
		if err != nil {
			return err
		}
		if err := json.Unmarshal(p.Manifest, &m); err != nil {
			return err
		}
		typ := "team"
		if c, err = tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: ws, Number: 1, Title: "t", Question: "q", OwnerID: u.ID,
			ScopeFrom: board.Grounding, ScopeTo: board.Grounding, AssigneeType: &typ, BoardColumn: board.Grounding, Round: 1}); err != nil {
			return err
		}
		if err := tx.Q.InsertCardTeam(ctx, db.InsertCardTeamParams{WorkspaceID: ws, CardID: c.ID, BoardColumn: board.Grounding, PackID: p.ID}); err != nil {
			return err
		}
		return nil
	})
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET study_cli = 'claude', auditor_cli = 'codex' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	in(u.ID, func(tx *store.Tx) error {
		c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: ws, ID: c.ID})
		if err != nil {
			return err
		}
		return e.Start(ctx, tx, c)
	})
	var harness, model string
	if err := env.Owner.QueryRow(ctx, `SELECT harness, requested_model FROM agent_run WHERE card_id = $1 AND role = 'leader'`, c.ID).Scan(&harness, &model); err != nil {
		t.Fatal(err)
	}
	if want := m.Leader.Agent.Models["codex"]; harness != "codex" || model != want || want == "" {
		t.Fatalf("leader run: %q %q", harness, model)
	}
}
