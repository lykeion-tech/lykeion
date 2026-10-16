-- name: CreateAutoresearch :one
INSERT INTO autoresearch (workspace_id, owner_id, title, description, execution_mode, title_template, budget_usd)
VALUES (@workspace_id, @owner_id, @title, @description, @execution_mode, @title_template, @budget_usd)
RETURNING *;

-- name: GetAutoresearch :one
SELECT * FROM autoresearch WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListAutoresearches :many
SELECT * FROM autoresearch WHERE workspace_id = @workspace_id ORDER BY created_at;

-- name: UpdateAutoresearch :one
UPDATE autoresearch SET title = @title, description = @description, title_template = @title_template, budget_usd = @budget_usd
WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: SetAutoresearchStatus :one
UPDATE autoresearch SET status = @status, pause_reason = @pause_reason
WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: DeleteAutoresearch :exec
DELETE FROM autoresearch WHERE workspace_id = @workspace_id AND id = @id;

-- name: IsCollaborator :one
SELECT EXISTS (SELECT 1 FROM autoresearch_collaborator
  WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id AND user_id = @user_id) AS ok;

-- name: AddCollaborator :exec
INSERT INTO autoresearch_collaborator (workspace_id, autoresearch_id, user_id) VALUES (@workspace_id, @autoresearch_id, @user_id)
ON CONFLICT DO NOTHING;

-- name: RemoveCollaborator :execrows
DELETE FROM autoresearch_collaborator WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id AND user_id = @user_id;

-- name: ListCollaborators :many
SELECT user_id FROM autoresearch_collaborator WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id ORDER BY user_id;

-- name: AddSubscriber :exec
INSERT INTO autoresearch_subscriber (workspace_id, autoresearch_id, user_id) VALUES (@workspace_id, @autoresearch_id, @user_id)
ON CONFLICT DO NOTHING;

-- name: RemoveSubscriber :execrows
DELETE FROM autoresearch_subscriber WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id AND user_id = @user_id;

-- name: ListSubscribers :many
SELECT user_id FROM autoresearch_subscriber WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id ORDER BY user_id;

-- The owner and the subscribers who are still members of the workspace.
-- name: AutoresearchRecipients :many
SELECT m.user_id FROM member m
WHERE m.workspace_id = @workspace_id AND (
  m.user_id = (SELECT a.owner_id FROM autoresearch a WHERE a.workspace_id = @workspace_id AND a.id = @autoresearch_id)
  OR m.user_id IN (SELECT s.user_id FROM autoresearch_subscriber s WHERE s.workspace_id = @workspace_id AND s.autoresearch_id = @autoresearch_id))
ORDER BY m.user_id;

-- name: CreateTrigger :one
INSERT INTO autoresearch_trigger (id, workspace_id, autoresearch_id, kind, cron, tz, next_run_at, token_hash, signing_secret_enc,
  provider, filters, created_by)
VALUES (@id, @workspace_id, @autoresearch_id, @kind, @cron, @tz, sqlc.narg(next_run_at), sqlc.narg(token_hash),
  sqlc.narg(signing_secret_enc), @provider, @filters, @created_by)
RETURNING *;

-- name: GetTrigger :one
SELECT * FROM autoresearch_trigger WHERE workspace_id = @workspace_id AND id = @id;

-- name: LockTrigger :one
SELECT * FROM autoresearch_trigger WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: ListTriggers :many
SELECT * FROM autoresearch_trigger WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id ORDER BY created_at;

-- name: SetTriggerNextRun :exec
UPDATE autoresearch_trigger SET next_run_at = sqlc.narg(next_run_at) WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetTriggerEnabled :one
UPDATE autoresearch_trigger SET enabled = @enabled WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: DeleteTrigger :execrows
DELETE FROM autoresearch_trigger WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id AND id = @id;

-- name: AutoresearchWebhookLookup :one
SELECT trigger_id::uuid AS trigger_id, workspace_id::uuid AS workspace_id FROM autoresearch_webhook_lookup(@token_hash::text);

-- name: AutoresearchDue :many
SELECT workspace_id::uuid AS workspace_id, trigger_id::uuid AS trigger_id FROM autoresearch_due(@at::timestamptz);

-- name: AutoresearchStaleRuns :many
SELECT workspace_id::uuid AS workspace_id, run_id::uuid AS run_id FROM autoresearch_stale_runs(@at::timestamptz);

-- name: AutoresearchDrift :many
SELECT workspace_id::uuid AS workspace_id, run_id::uuid AS run_id, status::text AS status, reason::text AS reason
FROM autoresearch_drift();

-- name: ClaimWebhookDeliveries :many
SELECT workspace_id::uuid AS workspace_id, delivery_id::uuid AS delivery_id FROM claim_webhook_deliveries(@n::int);

-- name: InsertRun :one
INSERT INTO autoresearch_run (workspace_id, autoresearch_id, trigger_id, source, acting_user_id, planned_at, status, reason,
  payload, idempotency_key)
VALUES (@workspace_id, @autoresearch_id, sqlc.narg(trigger_id), @source, @acting_user_id, @planned_at, @status, @reason,
  @payload, @idempotency_key)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: RunByKey :one
SELECT * FROM autoresearch_run WHERE workspace_id = @workspace_id AND idempotency_key = @idempotency_key;

-- name: GetRun :one
SELECT * FROM autoresearch_run WHERE workspace_id = @workspace_id AND id = @id;

-- name: LockRun :one
SELECT * FROM autoresearch_run WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: RunByAgentRun :one
SELECT * FROM autoresearch_run WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id::uuid FOR UPDATE;

-- name: ListRuns :many
SELECT * FROM autoresearch_run WHERE workspace_id = @workspace_id AND autoresearch_id = @autoresearch_id
ORDER BY created_at DESC LIMIT 100;

-- name: SetRun :exec
UPDATE autoresearch_run SET status = @status, reason = @reason, card_id = sqlc.narg(card_id), agent_run_id = sqlc.narg(agent_run_id),
  updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- Only a run still following its card or agent run moves; a final status stays.
-- name: FollowRun :execrows
UPDATE autoresearch_run SET status = @status, reason = @reason, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND status IN ('running', 'blocked');

-- name: SetRunReport :exec
UPDATE autoresearch_run SET report = @report, updated_at = now() WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertDelivery :one
INSERT INTO webhook_delivery (workspace_id, trigger_id, delivery_key, envelope)
VALUES (@workspace_id, @trigger_id, @delivery_key, @envelope)
ON CONFLICT (trigger_id, delivery_key) DO NOTHING
RETURNING *;

-- name: LockDelivery :one
SELECT * FROM webhook_delivery WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: FinishDelivery :exec
UPDATE webhook_delivery SET state = @state, run_id = sqlc.narg(run_id), lease_until = NULL
WHERE workspace_id = @workspace_id AND id = @id;

-- A delivery whose dispatch failed waits 30 seconds before the next attempt.
-- name: ReleaseDelivery :exec
UPDATE webhook_delivery SET state = 'pending', lease_until = now() + interval '30 seconds'
WHERE workspace_id = @workspace_id AND id = @id AND state = 'dispatching';

-- name: ListDeliveries :many
SELECT * FROM webhook_delivery WHERE workspace_id = @workspace_id AND trigger_id = @trigger_id ORDER BY received_at;

-- name: SetCardOrigin :exec
UPDATE card SET origin = @origin WHERE workspace_id = @workspace_id AND id = @id;

-- name: ActiveOriginDuplicate :one
SELECT id, number FROM card
WHERE workspace_id = @workspace_id AND origin = @origin AND ending = '' AND norm_text(title) = norm_text(@title::text)
ORDER BY number LIMIT 1;

-- name: LastOriginCard :one
SELECT id FROM card WHERE workspace_id = @workspace_id AND origin = @origin ORDER BY number DESC LIMIT 1;

-- name: CardTriggerPayload :one
SELECT body FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'trigger_payload'
ORDER BY created_at DESC LIMIT 1;

-- name: HasOnlineRuntime :one
SELECT EXISTS (SELECT 1 FROM runtime WHERE workspace_id = @workspace_id AND owner_id = @owner_id
  AND harness = @harness AND status = 'online') AS ok;

-- An autoresearch agent run: no card, claimed by its requester's runtimes, paid by them.
-- Its continuity scope stays empty: one-shot, never resumed.
-- name: EnqueueAutoresearchAgentRun :one
INSERT INTO agent_run (workspace_id, kind, requester_id, responsible_user_id, board_column, round, role, harness, prompt, skill, max_attempts, run_private)
VALUES (@workspace_id, 'autoresearch', @requester_id::uuid, @requester_id::uuid, '', 0, 'autoresearch', @harness, @prompt, @skill, 2, @run_private)
RETURNING *;

-- name: InsertAutoresearchInboxItem :exec
INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, autoresearch_run_id)
VALUES (@workspace_id, sqlc.narg(card_id), @user_id, @kind, @text, @autoresearch_run_id::uuid);
