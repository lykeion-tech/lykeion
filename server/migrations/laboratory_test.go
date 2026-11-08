package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestAgentStageAcceptsOnlyStages(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, _ := seedWorkspace(t, env)
	ins := `INSERT INTO agent (workspace_id, name, harness, owner_id, stage) VALUES ($1, $2, 'claude', $4, $3)`
	for i, stage := range []string{"", "grounding", "hypotheses", "experimentation", "validation"} {
		if _, err := env.Owner.Exec(t.Context(), ins, ws, "a"+string(rune('a'+i)), stage, u); err != nil {
			t.Errorf("stage %q: %v", stage, err)
		}
	}
	_, err := env.Owner.Exec(t.Context(), ins, ws, "bad", "reporting", u)
	wantCode(t, err, "23514", "stage reporting")
}

func TestLaboratoryAgentNeedsStageAndWorkspaceVisibility(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, _ := seedWorkspace(t, env)
	ins := `INSERT INTO agent (workspace_id, name, harness, owner_id, stage, visibility, laboratory) VALUES ($1, $2, 'claude', $3, $4, $5, 'built-in')`
	_, err := env.Owner.Exec(t.Context(), ins, ws, "nostage", u, "", "workspace")
	wantCode(t, err, "23514", "no stage")
	_, err = env.Owner.Exec(t.Context(), ins, ws, "private", u, "grounding", "private")
	wantCode(t, err, "23514", "private")
	if _, err := env.Owner.Exec(t.Context(), ins, ws, "ok", u, "grounding", "workspace"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedTeamMemberIsUnique(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, _ := seedWorkspace(t, env)
	exec := func(sql string, args ...any) error {
		_, err := env.Owner.Exec(t.Context(), sql, args...)
		return err
	}
	var pack, agent uuid.UUID
	if err := env.Owner.QueryRow(t.Context(), `INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, stage)
		VALUES ($1, 'P', '1', 'h', '{}', 'k', 'shipped', 'grounding') RETURNING id`, ws).Scan(&pack); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(t.Context(), `INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, 'a', 'claude', $2) RETURNING id`, ws, u).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'm', $4)`
	if err := exec(ins, ws, pack, nil, agent); err != nil {
		t.Fatal(err)
	}
	wantCode(t, exec(ins, ws, pack, nil, agent), "23505", "second shared row")
	if err := exec(ins, ws, pack, u, agent); err != nil {
		t.Fatalf("per-user row beside shared: %v", err)
	}
}

func TestWorkspaceLaboratoryDefaultsBuiltIn(t *testing.T) {
	env := testutil.Setup(t)
	_, ws, _ := seedWorkspace(t, env)
	var lab string
	if err := env.Owner.QueryRow(t.Context(), `SELECT laboratory FROM workspace WHERE id = $1`, ws).Scan(&lab); err != nil || lab != "built-in" {
		t.Fatalf("laboratory %q %v", lab, err)
	}
	_, err := env.Owner.Exec(t.Context(), `UPDATE workspace SET laboratory = 'other' WHERE id = $1`, ws)
	wantCode(t, err, "23514", "laboratory other")
}

func TestMigrationBackfillsCopyStages(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, _ := seedWorkspace(t, env)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	migration := func(name string) {
		t.Helper()
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		exec(string(b))
	}
	migration("0066_laboratory.down.sql")
	var pack, copied, loose uuid.UUID
	if err := env.Owner.QueryRow(t.Context(), `INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, stage)
		VALUES ($1, 'P', '1', 'h', '{}', 'k', 'shipped', 'grounding') RETURNING id`, ws).Scan(&pack); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, $2, 'claude', $3) RETURNING id`
	if err := env.Owner.QueryRow(t.Context(), ins, ws, "copy", u).Scan(&copied); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(t.Context(), ins, ws, "loose", u).Scan(&loose); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'm', $4)`, ws, pack, u, copied)
	migration("0066_laboratory.up.sql")
	for id, want := range map[uuid.UUID]string{copied: "grounding", loose: ""} {
		var got string
		if err := env.Owner.QueryRow(t.Context(), `SELECT stage FROM agent WHERE id = $1`, id).Scan(&got); err != nil || got != want {
			t.Errorf("agent stage %q, want %q (%v)", got, want, err)
		}
	}
}
