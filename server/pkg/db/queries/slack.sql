-- name: GetSlackInstallation :one
SELECT * FROM slack_installation WHERE workspace_id = app_workspace();

-- name: GetSlackInstallationByID :one
SELECT * FROM slack_installation WHERE workspace_id = app_workspace() AND id = @id;

-- name: CreateSlackInstallation :one
INSERT INTO slack_installation (id, workspace_id, app_id, team_id, team_name, bot_user_id, bot_token_ciphertext, app_token_ciphertext, drafting_agent_id, installed_by)
VALUES (@id, app_workspace(), @app_id, @team_id, @team_name, @bot_user_id, @bot_token_ciphertext, @app_token_ciphertext, @drafting_agent_id, @installed_by)
RETURNING *;

-- A revoked installation reconnected with the same app keeps its id, so its
-- bindings and chats survive.
-- name: ReconnectSlackInstallation :one
UPDATE slack_installation SET team_id = @team_id, team_name = @team_name, bot_user_id = @bot_user_id,
  bot_token_ciphertext = @bot_token_ciphertext, app_token_ciphertext = @app_token_ciphertext,
  drafting_agent_id = @drafting_agent_id, installed_by = @installed_by, status = 'active', updated_at = now()
WHERE workspace_id = app_workspace() AND id = @id AND status = 'revoked' RETURNING *;

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
INSERT INTO slack_post (workspace_id, installation_id, user_id, channel, thread_ts, message_ts, kind, agent_run_id, autoresearch_id)
VALUES (app_workspace(), @installation_id, @user_id, @channel, @thread_ts, @message_ts, @kind, @agent_run_id, @autoresearch_id)
RETURNING *;

-- name: ListPendingSlackPosts :many
SELECT * FROM slack_post
WHERE workspace_id = app_workspace() AND installation_id = @installation_id AND status = 'pending'
ORDER BY created_at;

-- name: MarkSlackPost :exec
UPDATE slack_post SET status = @status WHERE workspace_id = app_workspace() AND id = @id;

-- Compare-and-set out of 'pending': only one worker wins a post.
-- name: ClaimSlackPost :execrows
UPDATE slack_post SET status = @status
WHERE workspace_id = app_workspace() AND id = @id AND status = 'pending';

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

-- name: SetSlackDraftingAgent :one
UPDATE slack_installation SET drafting_agent_id = @drafting_agent_id, updated_at = now()
WHERE workspace_id = app_workspace() RETURNING *;

-- name: GetSlackDraftState :one
SELECT d.completed, d.card_id,
  COALESCE((SELECT r.status IN ('failed', 'skipped') FROM autoresearch_run r WHERE r.workspace_id = d.workspace_id AND r.autoresearch_id = d.autoresearch_id ORDER BY r.created_at DESC LIMIT 1), false)::boolean AS failed,
  COALESCE((SELECT r.reason FROM autoresearch_run r WHERE r.workspace_id = d.workspace_id AND r.autoresearch_id = d.autoresearch_id ORDER BY r.created_at DESC LIMIT 1), '')::text AS reason
FROM study_draft_request d
WHERE d.workspace_id = app_workspace() AND d.autoresearch_id = @autoresearch_id;

-- name: SlackActiveInstallations :many
-- Cross-tenant on purpose: ids only, through the SECURITY DEFINER function.
SELECT id::uuid AS id, workspace_id::uuid AS workspace_id FROM slack_active_installations();
