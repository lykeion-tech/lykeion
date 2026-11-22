-- name: UpsertGithubConnection :one
INSERT INTO github_connection (workspace_id, installation_id, account_login, account_type, account_avatar_url, connected_by)
VALUES (@workspace_id, @installation_id, @account_login, @account_type, @account_avatar_url, @connected_by)
ON CONFLICT (workspace_id, installation_id) DO UPDATE SET
  account_login = EXCLUDED.account_login,
  account_type = EXCLUDED.account_type,
  account_avatar_url = EXCLUDED.account_avatar_url,
  connected_by = EXCLUDED.connected_by,
  status = 'active',
  import_state = 'pending',
  updated_at = now()
RETURNING *;

-- name: ListGithubConnections :many
SELECT * FROM github_connection WHERE workspace_id = @workspace_id
ORDER BY created_at, installation_id;

-- name: GetGithubConnection :one
SELECT * FROM github_connection WHERE workspace_id = @workspace_id AND installation_id = @installation_id;

-- name: LockActiveGithubConnection :one
SELECT * FROM github_connection
WHERE workspace_id = @workspace_id AND installation_id = @installation_id AND status = 'active'
FOR UPDATE;

-- name: DeleteGithubConnection :execrows
DELETE FROM github_connection WHERE workspace_id = @workspace_id AND installation_id = @installation_id;

-- name: SetGithubConnectionStatus :execrows
UPDATE github_connection SET status = @status, updated_at = now()
WHERE workspace_id = @workspace_id AND installation_id = @installation_id AND status <> @status;

-- name: SetGithubImportState :execrows
UPDATE github_connection SET import_state = @import_state, updated_at = now()
WHERE workspace_id = @workspace_id AND installation_id = @installation_id AND status = 'active';

-- name: UpsertGithubPullRequest :one
INSERT INTO github_pull_request (
  workspace_id, installation_id, repo_owner, repo_name, number, title, state, html_url, branch, body_keys,
  author_login, author_avatar_url, additions, deletions, changed_files, head_sha, merged_at, closed_at,
  pr_created_at, pr_updated_at
) VALUES (
  @workspace_id, @installation_id, lower(@repo_owner::text), lower(@repo_name::text), @number, @title, @state, @html_url,
  @branch, COALESCE(@body_keys::text[], '{}'), @author_login, @author_avatar_url, @additions, @deletions, @changed_files,
  @head_sha, sqlc.narg(merged_at), sqlc.narg(closed_at), @pr_created_at, @pr_updated_at
)
ON CONFLICT (workspace_id, repo_owner, repo_name, number) DO UPDATE SET
  installation_id = EXCLUDED.installation_id,
  title = EXCLUDED.title,
  state = EXCLUDED.state,
  html_url = EXCLUDED.html_url,
  branch = EXCLUDED.branch,
  body_keys = EXCLUDED.body_keys,
  author_login = EXCLUDED.author_login,
  author_avatar_url = EXCLUDED.author_avatar_url,
  additions = EXCLUDED.additions,
  deletions = EXCLUDED.deletions,
  changed_files = EXCLUDED.changed_files,
  head_sha = EXCLUDED.head_sha,
  merged_at = EXCLUDED.merged_at,
  closed_at = EXCLUDED.closed_at,
  pr_created_at = EXCLUDED.pr_created_at,
  pr_updated_at = EXCLUDED.pr_updated_at,
  snapshot_stale = github_pull_request.snapshot_stale OR (
    github_pull_request.snapshot_at IS NOT NULL AND github_pull_request.head_sha <> EXCLUDED.head_sha
  ),
  updated_at = now()
RETURNING *;

-- name: GetGithubPullRequestByRepo :one
SELECT * FROM github_pull_request
WHERE workspace_id = @workspace_id AND repo_owner = lower(@repo_owner::text)
  AND repo_name = lower(@repo_name::text) AND number = @number;

-- name: SetGithubSnapshot :execrows
UPDATE github_pull_request SET ci_state = sqlc.narg(ci_state), ci_passed = @ci_passed, ci_total = @ci_total,
  ci_failing = COALESCE(@ci_failing::text[], '{}'), mergeability = sqlc.narg(mergeability),
  snapshot_at = now(), snapshot_stale = false, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND installation_id = @installation_id
  AND head_sha = @head_sha AND state IN ('open', 'draft');

-- name: MarkGithubSnapshotStale :execrows
UPDATE github_pull_request SET snapshot_stale = true, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND installation_id = @installation_id
  AND head_sha = @head_sha AND state IN ('open', 'draft');

-- name: ListPullRequestsForCard :many
SELECT sqlc.embed(p), l.source
FROM github_pull_request p
JOIN github_pr_link l ON l.workspace_id = p.workspace_id AND l.pr_id = p.id
WHERE l.workspace_id = @workspace_id AND l.card_id = @card_id
ORDER BY (p.state IN ('open', 'draft')) DESC, p.pr_updated_at DESC, p.id;

-- name: ListLinksForPR :many
SELECT * FROM github_pr_link WHERE workspace_id = @workspace_id AND pr_id = @pr_id
ORDER BY linked_at, card_id;

-- name: InsertGithubLink :execrows
INSERT INTO github_pr_link (workspace_id, card_id, pr_id, source, matched_key, linked_by)
VALUES (@workspace_id, @card_id, @pr_id, @source, @matched_key, sqlc.narg(linked_by))
ON CONFLICT (card_id, pr_id) DO NOTHING;

-- name: DeleteGithubLink :execrows
DELETE FROM github_pr_link WHERE workspace_id = @workspace_id AND card_id = @card_id AND pr_id = @pr_id;

-- name: InsertGithubUnlink :exec
INSERT INTO github_pr_unlink (workspace_id, card_id, pr_id)
VALUES (@workspace_id, @card_id, @pr_id)
ON CONFLICT (card_id, pr_id) DO NOTHING;

-- name: DeleteGithubUnlink :exec
DELETE FROM github_pr_unlink WHERE workspace_id = @workspace_id AND card_id = @card_id AND pr_id = @pr_id;

-- name: IsGithubUnlinked :one
SELECT EXISTS (
  SELECT 1 FROM github_pr_unlink WHERE workspace_id = @workspace_id AND card_id = @card_id AND pr_id = @pr_id
);

-- name: InsertWebhookDelivery :one
INSERT INTO github_webhook_delivery (id) VALUES (@id) ON CONFLICT (id) DO NOTHING RETURNING id;

-- name: DeleteWebhookDelivery :exec
DELETE FROM github_webhook_delivery WHERE id = @id;

-- name: PruneWebhookDeliveries :exec
DELETE FROM github_webhook_delivery WHERE received_at < now() - interval '7 days';

-- name: GithubInstallationWorkspaces :many
SELECT workspace_id::uuid AS workspace_id, card_prefix::text AS card_prefix, slug::text AS slug
FROM github_installation_workspaces(@installation_id::bigint);

-- name: GithubInstallationLifecycleWorkspaces :many
SELECT workspace_id::uuid AS workspace_id FROM github_installation_lifecycle_workspaces(@installation_id::bigint);

-- name: GithubPendingImports :many
SELECT workspace_id::uuid AS workspace_id, installation_id::bigint AS installation_id FROM github_pending_imports();

-- name: GetGithubAutoLink :one
SELECT COALESCE((settings->>'github_auto_link')::boolean, true)::boolean AS enabled
FROM workspace WHERE id = @workspace_id;

-- name: SetGithubAutoLink :exec
UPDATE workspace SET settings = jsonb_set(settings, '{github_auto_link}', to_jsonb(@enabled::boolean))
WHERE id = @workspace_id;
