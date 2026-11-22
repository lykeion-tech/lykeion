-- name: GetSlackInstallation :one
SELECT * FROM slack_installation WHERE workspace_id = app_workspace();

-- name: GetSlackInstallationByID :one
SELECT * FROM slack_installation WHERE workspace_id = app_workspace() AND id = @id;

-- name: CreateSlackInstallation :one
INSERT INTO slack_installation (workspace_id, app_id, team_id, team_name, bot_user_id, bot_token_ciphertext, app_token_ciphertext, drafting_agent_id, installed_by)
VALUES (app_workspace(), @app_id, @team_id, @team_name, @bot_user_id, @bot_token_ciphertext, @app_token_ciphertext, @drafting_agent_id, @installed_by)
RETURNING *;

-- name: SetSlackInstallationStatus :one
UPDATE slack_installation SET status = @status, updated_at = now()
WHERE workspace_id = app_workspace() AND id = @id RETURNING *;

-- name: DeleteSlackInstallation :exec
DELETE FROM slack_installation WHERE workspace_id = app_workspace() AND id = @id;

-- name: GetSlackBinding :one
SELECT * FROM slack_binding
WHERE workspace_id = app_workspace() AND installation_id = @installation_id AND slack_user_id = @slack_user_id;

-- name: CreateSlackBinding :one
INSERT INTO slack_binding (workspace_id, installation_id, slack_user_id, user_id)
VALUES (app_workspace(), @installation_id, @slack_user_id, @user_id) RETURNING *;

-- name: ListMySlackBindings :many
SELECT * FROM slack_binding WHERE workspace_id = app_workspace() AND user_id = app_user() ORDER BY created_at;

-- name: DeleteMySlackBinding :execrows
DELETE FROM slack_binding
WHERE workspace_id = app_workspace() AND user_id = app_user()
  AND installation_id = @installation_id AND slack_user_id = @slack_user_id;

-- name: CreateSlackLinkToken :exec
INSERT INTO slack_link_token (workspace_id, installation_id, slack_user_id, token_hash, expires_at)
VALUES (app_workspace(), @installation_id, @slack_user_id, @token_hash, @expires_at);

-- name: UseSlackLinkToken :one
UPDATE slack_link_token SET used_at = now()
WHERE workspace_id = app_workspace() AND token_hash = @h AND used_at IS NULL AND expires_at > now()
RETURNING *;

-- name: GetSlackChat :one
SELECT * FROM slack_chat
WHERE workspace_id = app_workspace() AND installation_id = @installation_id AND user_id = @user_id
  AND channel = @channel AND thread_ts = @thread_ts;

-- name: UpsertSlackChat :one
INSERT INTO slack_chat (workspace_id, installation_id, user_id, channel, thread_ts, lab_chat_id)
VALUES (app_workspace(), @installation_id, @user_id, @channel, @thread_ts, @lab_chat_id)
ON CONFLICT (installation_id, user_id, channel, thread_ts) DO UPDATE SET lab_chat_id = EXCLUDED.lab_chat_id
RETURNING *;

-- The sender's current DM chat is the row with an empty thread_ts.
-- name: DeleteSlackDMChat :exec
DELETE FROM slack_chat
WHERE workspace_id = app_workspace() AND installation_id = @installation_id AND user_id = @user_id
  AND channel = @channel AND thread_ts = '';

-- name: CreateSlackPost :one
INSERT INTO slack_post (workspace_id, installation_id, user_id, channel, thread_ts, kind, agent_run_id, autoresearch_id)
VALUES (app_workspace(), @installation_id, @user_id, @channel, @thread_ts, @kind, @agent_run_id, @autoresearch_id)
RETURNING *;

-- name: ListPendingSlackPosts :many
SELECT * FROM slack_post
WHERE workspace_id = app_workspace() AND installation_id = @installation_id AND status = 'pending'
ORDER BY created_at;

-- name: MarkSlackPost :exec
UPDATE slack_post SET status = @status WHERE workspace_id = app_workspace() AND id = @id;

-- name: BumpSlackPostAttempts :one
UPDATE slack_post SET attempts = attempts + 1
WHERE workspace_id = app_workspace() AND id = @id RETURNING attempts;

-- name: ListSlackRunIDs :many
SELECT agent_run_id::uuid FROM slack_post
WHERE workspace_id = app_workspace() AND installation_id = @installation_id
  AND status IN ('pending', 'sending') AND agent_run_id IS NOT NULL;

-- name: GetLabResultForRun :one
SELECT * FROM lab_message
WHERE workspace_id = app_workspace() AND agent_run_id = @agent_run_id AND kind IN ('answer', 'failure')
ORDER BY created_at DESC LIMIT 1;
