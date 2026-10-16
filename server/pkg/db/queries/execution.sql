-- name: LockWorkspaceExecution :one
-- Serialize execution batches without blocking workspace foreign-key checks.
SELECT id FROM workspace WHERE id = @id FOR NO KEY UPDATE;

-- name: UpsertRuntime :one
INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness, device_name, cli_version, status, last_seen_at)
VALUES (@workspace_id, @owner_id, @daemon_id, @harness, @device_name, @cli_version, 'online', now())
ON CONFLICT (workspace_id, daemon_id, harness) DO UPDATE
  SET device_name = EXCLUDED.device_name, cli_version = EXCLUDED.cli_version, status = 'online', last_seen_at = now()
  WHERE runtime.owner_id = EXCLUDED.owner_id
RETURNING *;

-- name: TouchRuntime :execrows
UPDATE runtime SET status = 'online', last_seen_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND owner_id = @owner_id;

-- name: GetRuntime :one
SELECT * FROM runtime WHERE id = @id AND workspace_id = @workspace_id;

-- active_runs: the runtime's agent runs that are dispatched or running.
-- name: ListRuntimes :many
SELECT r.*, (SELECT count(*) FROM agent_run t WHERE t.workspace_id = r.workspace_id AND t.runtime_id = r.id
  AND t.status IN ('dispatched', 'running'))::int AS active_runs
FROM runtime r WHERE r.workspace_id = @workspace_id ORDER BY r.created_at;

-- name: SetRuntimeConcurrency :execrows
UPDATE runtime SET max_concurrent = @max_concurrent
WHERE id = @id AND workspace_id = @workspace_id AND owner_id = @owner_id;

-- name: SetRuntimeName :execrows
UPDATE runtime SET custom_name = sqlc.narg(custom_name)
WHERE id = @id AND workspace_id = @workspace_id AND owner_id = @owner_id;

-- name: DeleteRuntime :execrows
DELETE FROM runtime WHERE id = @id AND workspace_id = @workspace_id AND owner_id = @owner_id;

-- name: EnqueueAgentRun :one
INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, skill, max_attempts, continuity_scope, run_private)
VALUES (@workspace_id, @card_id, @responsible_user_id, @board_column, @round, @role, @harness, @prompt, @skill, @max_attempts, @continuity_scope, @run_private)
RETURNING *;

-- name: ClaimAgentRun :one
UPDATE agent_run SET status = 'dispatched', runtime_id = @runtime_id, dispatched_at = now(), queue_reason = NULL,
  prepare_lease_expires_at = now() + interval '2 minutes', attempt = attempt + 1
WHERE id = (
  SELECT t.id FROM agent_run t
  JOIN runtime r ON r.id = @runtime_id AND r.workspace_id = t.workspace_id
  LEFT JOIN card c ON c.id = t.card_id
  WHERE t.workspace_id = @workspace_id AND t.status = 'queued'
    AND t.harness = r.harness AND r.status = 'online'
    AND (
      -- a role run runs on its card owner's runtimes while the card is clear
      (t.kind = 'role' AND c.owner_id = r.owner_id AND c.flag = '' AND c.ending = '')
      -- an autoresearch agent run has no card and runs on its requester's runtimes
      OR (t.kind = 'autoresearch' AND t.requester_id = r.owner_id))
  ORDER BY t.created_at
  FOR UPDATE OF t SKIP LOCKED
  LIMIT 1
)
RETURNING *;

-- name: InsertAgentRunToken :exec
INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, scopes, expires_at)
VALUES (@token_hash, @workspace_id, @agent_run_id, @card_id, @role, @attempt, @scopes, @expires_at);

-- name: ExtendLease :execrows
UPDATE agent_run SET prepare_lease_expires_at = now() + interval '2 minutes'
WHERE id = @id AND workspace_id = @workspace_id AND runtime_id = @runtime_id AND status = 'dispatched';

-- name: StartAgentRun :execrows
UPDATE agent_run SET status = 'running', started_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND runtime_id = @runtime_id
  AND status = 'dispatched' AND prepare_lease_expires_at > now();

-- name: GetAgentRun :one
SELECT * FROM agent_run WHERE id = @id AND workspace_id = @workspace_id;

-- name: LockAgentRun :one
SELECT * FROM agent_run WHERE id = @id AND workspace_id = @workspace_id FOR UPDATE;

-- name: InsertAgentRunMessage :exec
INSERT INTO agent_run_message (workspace_id, agent_run_id, attempt, seq, type, tool, content)
VALUES (@workspace_id, @agent_run_id, @attempt, @seq, @type, @tool, @content)
ON CONFLICT (agent_run_id, attempt, seq) DO NOTHING;

-- name: AgentRunMessagesAfter :many
SELECT * FROM agent_run_message WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id AND attempt = @attempt AND seq > @after
ORDER BY seq LIMIT 500;

-- name: FinishAgentRun :exec
UPDATE agent_run SET status = @status, completed_at = now(), cost_usd = cost_usd + @cost_usd,
  session_id = @session_id, error = @error, failure_reason = sqlc.narg(failure_reason)
WHERE id = @id AND workspace_id = @workspace_id;

-- name: RecordAgentRunProfile :exec
UPDATE agent_run SET profile_mode = @profile_mode, policy_digest = @policy_digest, model = @model
WHERE id = @id AND workspace_id = @workspace_id;

-- name: RequeueAgentRun :exec
UPDATE agent_run SET status = 'queued', runtime_id = NULL, prepare_lease_expires_at = NULL, started_at = NULL,
  cost_usd = cost_usd + @cost_usd, error = @error, failure_reason = @failure_reason
WHERE id = @id AND workspace_id = @workspace_id;

-- name: RecordLateUsage :exec
UPDATE agent_run SET cost_usd = cost_usd + @cost_usd, late_usage_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'cancelled';

-- name: InsertAgentRunUsage :exec
INSERT INTO agent_run_usage (workspace_id, agent_run_id, attempt, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, cost_reported)
VALUES (@workspace_id, @agent_run_id, @attempt, @model, @input_tokens, @output_tokens, @cache_read_tokens, @cache_write_tokens, @cost_usd, @cost_reported);

-- name: CancelAgentRun :execrows
UPDATE agent_run SET status = 'cancelled', completed_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status IN ('queued', 'dispatched', 'running');

-- name: AddCardSpend :exec
UPDATE card SET spent_usd = spent_usd + @usd, version = version + 1 WHERE id = @id AND workspace_id = @workspace_id;

-- name: DeleteAgentRunTokens :exec
DELETE FROM agent_run_token WHERE agent_run_id = @agent_run_id AND workspace_id = @workspace_id;

-- name: LookupAgentRunToken :one
SELECT agent_run_id::uuid AS agent_run_id, workspace_id::uuid AS workspace_id,
  COALESCE(card_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS card_id,
  role::text AS role, board_column::text AS board_column, round::int AS round, scopes::text[] AS scopes
FROM agent_run_token_lookup(@token_hash::text);

-- name: SweepRuntimes :one
SELECT sweep_runtimes(@stale::interval)::int AS n;

-- name: SweepAgentRuns :many
SELECT agent_run_id::uuid AS agent_run_id, workspace_id::uuid AS workspace_id, status::text AS status
FROM sweep_agent_runs();

-- name: InsertRecord :one
INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body, blob_key, agent_run_id)
VALUES (@workspace_id, @card_id, @board_column, @round, @kind, @role, @key, @body, @blob_key, @agent_run_id)
RETURNING *;

-- name: ListRecords :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
  AND superseded_at IS NULL ORDER BY created_at;

-- name: SupersedeRoleRecords :exec
UPDATE record SET superseded_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
  AND role = @role AND superseded_at IS NULL;

-- name: LockAgentRunToken :one
SELECT * FROM agent_run_token
WHERE token_hash = @token_hash AND workspace_id = @workspace_id AND agent_run_id = @agent_run_id
  AND expires_at > clock_timestamp()
FOR UPDATE;

-- name: RoleBlobExists :one
SELECT EXISTS (
  SELECT 1 FROM event WHERE workspace_id = @workspace_id AND card_id = @card_id
    AND type = 'blob.created' AND payload->>'blob_key' = @blob_key::text
)::boolean;
