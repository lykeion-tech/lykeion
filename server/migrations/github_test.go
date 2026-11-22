package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func execGithubFixture(t *testing.T, env *testutil.Env, query string, args ...any) {
	t.Helper()
	if _, err := env.Owner.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func seedGithubLinks(t *testing.T, env *testutil.Env, user, workspace, card uuid.UUID) {
	t.Helper()
	execGithubFixture(t, env, `INSERT INTO github_connection (workspace_id, installation_id, account_login, account_type, connected_by)
		VALUES ($1, 10, 'owner', 'User', $2)`, workspace, user)
	pr := uuid.New()
	execGithubFixture(t, env, `INSERT INTO github_pull_request (id, workspace_id, installation_id, repo_owner, repo_name, number, title, state, html_url, pr_created_at, pr_updated_at)
		VALUES ($1, $2, 10, 'owner', 'repo', 1, 'title', 'open', 'https://github.com/owner/repo/pull/1', now(), now())`, pr, workspace)
	execGithubFixture(t, env, `INSERT INTO github_pr_link (workspace_id, card_id, pr_id, source) VALUES ($1, $2, $3, 'auto')`, workspace, card, pr)
	execGithubFixture(t, env, `INSERT INTO github_pr_unlink (workspace_id, card_id, pr_id) VALUES ($1, $2, $3)`, workspace, card, pr)
}

func TestGithubTablesIsolated(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	user, a, card := seedWorkspace(t, env)
	b := uuid.New()
	execGithubFixture(t, env, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'other', 'Other')`, b)
	seedGithubLinks(t, env, user, a, card)

	for _, tc := range []struct {
		workspace uuid.UUID
		want      int
	}{{a, 1}, {b, 0}} {
		tx, err := env.App.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id', $1, true)`, tc.workspace.String()); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"github_connection", "github_pull_request", "github_pr_link", "github_pr_unlink"} {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != tc.want {
				t.Errorf("%s in workspace %s: count = %d, err = %v, want %d", table, tc.workspace, count, err, tc.want)
			}
		}
	}
}

func TestGithubLinkCascadesWithCard(t *testing.T) {
	env := testutil.Setup(t)
	user, workspace, card := seedWorkspace(t, env)
	seedGithubLinks(t, env, user, workspace, card)
	execGithubFixture(t, env, `DELETE FROM card WHERE id = $1`, card)
	for _, table := range []string{"github_pr_link", "github_pr_unlink"} {
		var count int
		if err := env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s after deleting the card: count = %d, err = %v, want zero", table, count, err)
		}
	}
}

func TestGithubDisconnectRetainsLinkedPullRequests(t *testing.T) {
	env := testutil.Setup(t)
	user, workspace, card := seedWorkspace(t, env)
	seedGithubLinks(t, env, user, workspace, card)
	execGithubFixture(t, env, `DELETE FROM github_connection WHERE workspace_id = $1 AND installation_id = 10`, workspace)
	var count int
	if err := env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM github_pr_link JOIN github_pull_request ON github_pull_request.id = github_pr_link.pr_id WHERE card_id = $1`, card).Scan(&count); err != nil || count != 1 {
		t.Fatalf("linked PRs after disconnect: count = %d, err = %v, want one", count, err)
	}
}

func TestInstallationWorkspacesFunction(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	user, active, _ := seedWorkspace(t, env)
	suspended := uuid.New()
	execGithubFixture(t, env, `UPDATE workspace SET card_prefix = 'FIX' WHERE id = $1`, active)
	execGithubFixture(t, env, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'suspended', 'Suspended')`, suspended)
	execGithubFixture(t, env, `INSERT INTO github_connection (workspace_id, installation_id, account_login, account_type, connected_by, status, import_state)
		VALUES ($1, 10, 'owner', 'User', $3, 'active', 'running'),
		       ($2, 10, 'owner', 'User', $3, 'suspended', 'pending'),
		       ($1, 11, 'other', 'User', $3, 'active', 'done')`, active, suspended, user)

	rows, err := env.App.Query(ctx, `SELECT workspace_id, card_prefix, slug FROM github_installation_workspaces(10)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("active workspace missing: %v", rows.Err())
	}
	var got uuid.UUID
	var prefix, slug string
	if err := rows.Scan(&got, &prefix, &slug); err != nil {
		t.Fatal(err)
	}
	if got != active || prefix != "FIX" || slug != "s" {
		t.Errorf("active workspace = (%s, %q, %q), want (%s, FIX, s)", got, prefix, slug, active)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("expected exactly one active workspace: %v", rows.Err())
	}
	var count int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM github_installation_lifecycle_workspaces(10)`).Scan(&count); err != nil || count != 2 {
		t.Errorf("lifecycle workspace count = %d, err = %v, want two", count, err)
	}
	var installationID int64
	if err := env.App.QueryRow(ctx, `SELECT workspace_id, installation_id FROM github_pending_imports()`).Scan(&got, &installationID); err != nil {
		t.Fatal(err)
	}
	if got != active || installationID != 10 {
		t.Errorf("pending import = (%s, %d), want (%s, 10)", got, installationID, active)
	}
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM github_pending_imports()`).Scan(&count); err != nil || count != 1 {
		t.Errorf("pending import count = %d, err = %v, want one", count, err)
	}
}

func TestGithubMigrationRollsBack(t *testing.T) {
	env := testutil.Setup(t)
	apply := func(direction string) {
		t.Helper()
		body, err := fs.ReadFile(migrations.FS, "0081_github."+direction+".sql")
		if err != nil {
			t.Fatal(err)
		}
		execGithubFixture(t, env, string(body))
	}
	apply("down")
	var dropped bool
	if err := env.Owner.QueryRow(t.Context(), `SELECT to_regclass('public.github_connection') IS NULL
		AND to_regprocedure('public.github_installation_workspaces(bigint)') IS NULL`).Scan(&dropped); err != nil || !dropped {
		t.Fatalf("rollback left the GitHub resolver or connection table: %v", err)
	}
	apply("up")
	user, workspace, card := seedWorkspace(t, env)
	seedGithubLinks(t, env, user, workspace, card)
}
