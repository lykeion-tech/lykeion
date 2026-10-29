-- name: LockWorkspaceExecution :one
-- Serialize execution batches without blocking workspace foreign-key checks.
SELECT id FROM workspace WHERE id = @id FOR NO KEY UPDATE;

-- name: UpsertRuntime :one
INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness, device_name, cli_version, os, arch, kernel, language, status, last_seen_at)
VALUES (@workspace_id, @owner_id, @daemon_id, @harness, @device_name, @cli_version, @os, @arch, @kernel, @language, 'online', now())
ON CONFLICT (workspace_id, daemon_id, harness) DO UPDATE
  SET device_name = EXCLUDED.device_name, cli_version = EXCLUDED.cli_version, os = EXCLUDED.os, arch = EXCLUDED.arch,
    kernel = EXCLUDED.kernel, language = EXCLUDED.language, status = 'online', last_seen_at = now()
  WHERE runtime.owner_id = EXCLUDED.owner_id
RETURNING *;

-- name: TouchRuntime :execrows
UPDATE runtime SET status = 'online', last_seen_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND owner_id = @owner_id;

-- name: SetRuntimeBases :execrows
UPDATE runtime SET bases = @bases
WHERE id = @id AND workspace_id = @workspace_id AND owner_id = @owner_id AND bases IS DISTINCT FROM @bases;

-- name: GetRuntime :one
SELECT * FROM runtime WHERE id = @id AND workspace_id = @workspace_id;

-- active_runs: the runtime's agent runs that are dispatched or running.
-- name: ListRuntimes :many
SELECT r.*, (SELECT count(*) FROM agent_run t WHERE t.workspace_id = r.workspace_id AND t.runtime_id = r.id
  AND t.status IN ('dispatched', 'running'))::int AS active_runs,
  COALESCE((SELECT array_agg(s.user_id ORDER BY s.user_id) FROM runtime_share s
    WHERE s.runtime_id = r.id AND s.workspace_id = r.workspace_id), '{}'::uuid[])::uuid[] AS shared_with
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
INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, skill, max_attempts, continuity_scope, requested_model, agent_id, assignment)
VALUES (@workspace_id, @card_id, @responsible_user_id, @board_column, @round, @role, @harness, @prompt, @skill, @max_attempts, @continuity_scope, @requested_model, sqlc.narg(agent_id), @assignment)
RETURNING *;

-- name: ClaimAgentRun :one
UPDATE agent_run SET status = 'dispatched', runtime_id = @runtime_id, dispatched_at = now(), queue_reason = NULL,
  prepare_lease_expires_at = now() + interval '2 minutes', attempt = attempt + 1
WHERE id = (
  SELECT t.id FROM agent_run t
  JOIN runtime r ON r.id = @runtime_id AND r.workspace_id = t.workspace_id
  LEFT JOIN card c ON c.id = t.card_id
  LEFT JOIN coresearch_chat cc ON cc.id=t.coresearch_chat_id AND cc.workspace_id=t.workspace_id
  LEFT JOIN coresearch_project cp ON cp.id=cc.project_id AND cp.workspace_id=t.workspace_id
  WHERE t.workspace_id = @workspace_id AND t.status = 'queued'
 AND ((@coresearch_only::boolean AND t.kind='coresearch') OR (NOT @coresearch_only::boolean AND t.kind IN ('role','autoresearch')))
    AND t.harness = r.harness AND r.status = 'online'
    AND (t.responsible_user_id = r.owner_id OR (@guest_isolation::boolean AND r.os = 'linux'))
    AND (
      -- role runs use the chosen machine with consent, or the card owner's runtimes
      (t.kind = 'role' AND t.responsible_user_id = c.owner_id AND c.flag = '' AND c.ending = ''
       AND EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = t.workspace_id AND m.user_id = c.owner_id) AND (
       (c.machine IS NULL AND c.owner_id = r.owner_id)
       OR (c.machine = r.daemon_id AND (r.owner_id = c.owner_id OR (@guest_isolation::boolean AND r.os = 'linux' AND (r.visibility = 'workspace'
           OR EXISTS (SELECT 1 FROM runtime_share s WHERE s.runtime_id = r.id AND s.user_id = c.owner_id)))))))
      -- an autoresearch agent run has no card and runs on its requester's runtimes
      OR (t.kind = 'autoresearch' AND t.requester_id = r.owner_id AND t.responsible_user_id = r.owner_id)
      OR (t.kind='coresearch' AND t.responsible_user_id=r.owner_id AND cc.owner_id=r.owner_id AND cc.cli=r.harness
          AND (cc.daemon_id IS NULL OR cc.daemon_id=r.daemon_id) AND cp.archived_at IS NULL
          -- a turn frozen to a model runs on a runtime whose CLI lists it; when none
          -- of the owner's online runtimes lists it, any runs it on the CLI default
          AND (t.requested_model='' OR r.models @> jsonb_build_array(jsonb_build_object('value', t.requested_model))
               OR NOT EXISTS(SELECT 1 FROM runtime o WHERE o.workspace_id=t.workspace_id AND o.owner_id=r.owner_id AND o.harness=r.harness
                             AND o.status='online' AND o.models @> jsonb_build_array(jsonb_build_object('value', t.requested_model))))
          AND EXISTS(SELECT 1 FROM coresearch_member cm WHERE cm.workspace_id=t.workspace_id AND cm.project_id=cc.project_id AND cm.user_id=cc.owner_id)))
  ORDER BY t.created_at
  FOR UPDATE OF t SKIP LOCKED
  LIMIT 1
)
RETURNING *, (SELECT machine FROM card WHERE id = agent_run.card_id) AS claimed_machine;

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

-- name: AgentRunMessage :one
SELECT * FROM agent_run_message
WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id AND attempt = @attempt AND seq = @seq;

-- name: LockRemovedHarnessQueue :many
SELECT t.* FROM agent_run t
LEFT JOIN card c ON c.id = t.card_id AND c.workspace_id = t.workspace_id
WHERE t.workspace_id = @workspace_id AND t.status = 'queued'
 AND ((@coresearch_only::boolean AND t.kind='coresearch') OR (NOT @coresearch_only::boolean AND t.kind IN ('role','autoresearch')))
 AND t.harness IN ('cursor', 'antigravity')
 AND ((t.kind = 'role' AND c.owner_id = @owner_id)
   OR (t.kind IN ('autoresearch','coresearch') AND t.requester_id = @owner_id))
ORDER BY t.id FOR UPDATE OF t;

-- name: SetRuntimeSharing :execrows
UPDATE runtime SET visibility = @visibility
WHERE id = @id AND workspace_id = @workspace_id AND owner_id = @owner_id;

-- name: ReplaceRuntimeShares :exec
WITH removed AS (
 DELETE FROM runtime_share WHERE runtime_id = @runtime_id AND workspace_id = @workspace_id RETURNING user_id
)
INSERT INTO runtime_share (workspace_id, runtime_id, user_id)
SELECT @workspace_id, @runtime_id, u.user_id FROM unnest(@user_ids::uuid[]) AS u(user_id)
WHERE (SELECT count(*) FROM removed) >= 0;

-- name: RuntimeShares :many
SELECT user_id FROM runtime_share WHERE runtime_id = @runtime_id AND workspace_id = @workspace_id ORDER BY user_id;

-- name: RuntimeDependentRuns :many
SELECT t.*, c.title AS study_title, u.name AS colleague
FROM agent_run t
JOIN card c ON c.id = t.card_id AND c.workspace_id = t.workspace_id
JOIN "user" u ON u.id = t.responsible_user_id
WHERE t.workspace_id = @workspace_id AND t.status IN ('queued','dispatched','running')
AND t.kind <> 'coresearch' AND t.responsible_user_id <> @owner_id
AND (t.runtime_id = @runtime_id OR (t.status = 'queued' AND t.kind = 'role' AND c.machine = @daemon_id AND t.harness = @harness))
ORDER BY t.id FOR UPDATE OF t;

-- name: SetRuntimeRevokedReason :exec
UPDATE agent_run SET cancel_reason = 'runtime_access_revoked' WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetRunMachine :one
UPDATE agent_run SET machine_name = COALESCE(r.custom_name, r.device_name), machine_owner_id = r.owner_id
FROM runtime r WHERE agent_run.id = @id AND agent_run.workspace_id = @workspace_id AND r.id = agent_run.runtime_id
RETURNING agent_run.*;

-- name: UndoClaimAttempt :exec
UPDATE agent_run SET attempt = attempt - 1 WHERE id = @id AND workspace_id = @workspace_id;

-- name: DepartingMemberSharedRuns :many
SELECT t.* FROM agent_run t
LEFT JOIN card c ON c.id = t.card_id AND c.workspace_id = t.workspace_id
WHERE t.workspace_id = @workspace_id AND t.status IN ('queued', 'dispatched', 'running')
AND t.kind <> 'coresearch'
AND EXISTS (
 SELECT 1 FROM runtime r WHERE r.workspace_id = t.workspace_id
 AND ((t.responsible_user_id = @user_id AND r.owner_id <> @user_id)
   OR (t.responsible_user_id <> @user_id AND r.owner_id = @user_id))
 AND (t.runtime_id = r.id OR (t.status = 'queued' AND t.kind = 'role'
      AND c.machine = r.daemon_id AND t.harness = r.harness))
)
ORDER BY t.id FOR UPDATE OF t;

-- name: ClearDepartingMemberRuntimeShares :exec
DELETE FROM runtime_share s USING runtime r
WHERE s.workspace_id = @workspace_id AND r.workspace_id = s.workspace_id
AND r.id = s.runtime_id AND r.owner_id = @user_id;

-- name: PrivatizeDepartingMemberRuntimes :exec
UPDATE runtime SET visibility = 'private' WHERE workspace_id = @workspace_id AND owner_id = @user_id;

-- name: AcceptMachineAttempt :exec
-- Called after delivery validation, atomically with attempt token issuance.
INSERT INTO agent_run_machine_attempt (workspace_id, agent_run_id, attempt, runtime_id, daemon_id, owner_id, responsible_user_id)
VALUES (@workspace_id, @agent_run_id, @attempt, @runtime_id, @daemon_id, @owner_id, @responsible_user_id);

-- name: StartMachineAttempt :exec
UPDATE agent_run_machine_attempt SET started_at = now()
WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id AND attempt = @attempt AND runtime_id = @runtime_id;

-- name: SetRuntimeCompute :exec
UPDATE runtime SET metrics=sqlc.narg(metrics),models=coalesce(sqlc.narg(models)::jsonb,models)
WHERE id= @id AND workspace_id= @workspace_id AND owner_id= @owner_id;
