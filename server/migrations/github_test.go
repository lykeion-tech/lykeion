package migrations_test

import (
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	db "lykeion/server/pkg/db/generated"
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
		       ($1, 11, 'other', 'User', $3, 'active', 'done'),
		       ($2, 12, 'other', 'User', $3, 'suspended', 'running'),
		       ($2, 13, 'other', 'User', $3, 'active', 'failed')`, active, suspended, user)

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
	unfinished, err := db.New(env.App).GithubPendingImports(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[db.GithubPendingImportsRow]bool{
		{WorkspaceID: active, InstallationID: 10}:    true,
		{WorkspaceID: suspended, InstallationID: 10}: true,
		{WorkspaceID: suspended, InstallationID: 12}: true,
	}
	for _, row := range unfinished {
		if !want[row] {
			t.Errorf("unexpected unfinished import: %+v", row)
		}
		delete(want, row)
	}
	if len(want) != 0 {
		t.Errorf("missing unfinished imports: %v", want)
	}
}

func TestGithubPullRequestUpsertRejectsOlderUpdate(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	_, workspace, _ := seedWorkspace(t, env)
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id', $1, true)`, workspace.String()); err != nil {
		t.Fatal(err)
	}

	q := db.New(tx)
	createdAt := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	initial := db.UpsertGithubPullRequestParams{
		WorkspaceID: workspace, InstallationID: 10, RepoOwner: "Owner", RepoName: "Repo",
		Number: 1, Title: "Merged", State: "merged", HtmlUrl: "https://github.com/owner/repo/pull/1",
		PrCreatedAt: createdAt, PrUpdatedAt: createdAt.Add(time.Hour),
	}
	inserted, err := q.UpsertGithubPullRequest(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}

	stale := initial
	stale.PrUpdatedAt = createdAt
	stale.State = "open"
	if _, err := q.UpsertGithubPullRequest(ctx, stale); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("older update: got %v, want ErrNoRows", err)
	}
	current, err := q.GetGithubPullRequestByRepo(ctx, db.GetGithubPullRequestByRepoParams{
		WorkspaceID: workspace, RepoOwner: "OWNER", RepoName: "REPO", Number: 1,
	})
	if err != nil || current.State != "merged" || !current.PrUpdatedAt.Equal(initial.PrUpdatedAt) {
		t.Fatalf("stored state after older update: state = %q, updated = %v, err = %v", current.State, current.PrUpdatedAt, err)
	}
	initial.Title = "Same-second edit"
	revised, err := q.UpsertGithubPullRequest(ctx, initial)
	if err != nil || revised.ID != inserted.ID || revised.Title != initial.Title {
		t.Fatalf("equal-timestamp update: id = %s, title = %q, err = %v", revised.ID, revised.Title, err)
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
