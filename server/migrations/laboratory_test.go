package migrations_test

import (
	"io/fs"
	"strings"
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
	migration("0067_laboratory_runtime.down.sql")
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

func TestRuntimeDeletionUnbinds(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, _ := seedWorkspace(t, env)
	ctx := t.Context()
	var rt, lab, own uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1, $2, 'd', 'claude') RETURNING id`, ws, u).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, harness, owner_id, stage, visibility, laboratory, runtime_id)
		VALUES ($1, 'lab', 'claude', $2, 'grounding', 'workspace', 'built-in', $3) RETURNING id`, ws, u, rt).Scan(&lab); err != nil {
		t.Fatal(err)
	}
	_, err := env.Owner.Exec(ctx, `INSERT INTO agent (workspace_id, name, harness, owner_id, runtime_id) VALUES ($1, 'own', 'claude', $2, $3)`, ws, u, rt)
	wantCode(t, err, "23514", "a researcher's agent bound to a runtime")
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, 'own', 'claude', $2) RETURNING id`, ws, u).Scan(&own); err != nil {
		t.Fatal(err)
	}
	_, err = env.Owner.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, lab, uuid.New())
	wantCode(t, err, "23503", "an unknown runtime")
	if _, err := env.Owner.Exec(ctx, `DELETE FROM runtime WHERE id = $1`, rt); err != nil {
		t.Fatal(err)
	}
	var bound *uuid.UUID
	var harness string
	if err := env.Owner.QueryRow(ctx, `SELECT runtime_id, harness FROM agent WHERE id = $1`, lab).Scan(&bound, &harness); err != nil {
		t.Fatal(err)
	}
	if bound != nil || harness != "claude" {
		t.Fatalf("after deletion: runtime %v harness %q", bound, harness)
	}
}

func TestMigrationGivesBuiltInAgentsWithoutAnIconOne(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, _ := seedWorkspace(t, env)
	ins := `INSERT INTO agent (workspace_id, name, harness, owner_id, stage, visibility, laboratory, avatar_url) VALUES ($1, $2, 'claude', $3, $4, $5, $6, $7) RETURNING id`
	var bare, kept, plain uuid.UUID
	for _, row := range []struct {
		id                                 *uuid.UUID
		name, stage, visibility, lab, icon string
	}{
		{&bare, "bare", "grounding", "workspace", "built-in", ""},
		{&kept, "kept", "grounding", "workspace", "built-in", "emoji:🦁"},
		{&plain, "plain", "", "private", "", ""},
	} {
		if err := env.Owner.QueryRow(t.Context(), ins, ws, row.name, u, row.stage, row.visibility, row.lab, row.icon).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	up, err := fs.ReadFile(migrations.FS, "0070_builtin_agent_icons.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	icon := func(id uuid.UUID) string {
		var got string
		if err := env.Owner.QueryRow(t.Context(), `SELECT avatar_url FROM agent WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	set := "🦊 🦁 🐼 🐸 🐴 🐝 🐛 🦋 🐞 🕷️ 🐢 🐍 🐙 🐬 🐯"
	if g, ok := strings.CutPrefix(icon(bare), "emoji:"); !ok || !strings.Contains(" "+set+" ", " "+g+" ") {
		t.Errorf("built-in agent without an icon got %q", icon(bare))
	}
	if got := icon(kept); got != "emoji:🦁" {
		t.Errorf("built-in agent's own icon changed to %q", got)
	}
	if got := icon(plain); got != "" {
		t.Errorf("a person's agent got icon %q", got)
	}
}
