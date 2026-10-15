-- name: RenewInputLease :exec
UPDATE agent_run SET input_lease_expires_at = now() + @lease::interval
WHERE id = @id AND workspace_id = @workspace_id;

-- Takes an unheld or expired fetch claim using a fresh acquisition identity.
-- name: ClaimInputFetch :execrows
UPDATE agent_run SET input_fetch_id = @fetch_id::uuid, input_fetch_expires_at = now() + @lease::interval
WHERE id = @id AND workspace_id = @workspace_id
  AND (input_fetch_expires_at IS NULL OR input_fetch_expires_at <= now());

-- Only the current owner can renew, while both leases remain live.
-- name: RenewInputFetch :execrows
UPDATE agent_run SET input_fetch_expires_at = now() + @lease::interval
WHERE id = @id AND workspace_id = @workspace_id AND input_fetch_id = @fetch_id::uuid
  AND input_fetch_expires_at > now() AND input_lease_expires_at > now();

-- An expired or replaced fetcher cannot publish outcomes.
-- name: RecordInputOutcome :execrows
UPDATE agent_run SET input_outcomes = input_outcomes || @outcome::jsonb
WHERE id = @id AND workspace_id = @workspace_id AND input_fetch_id = @fetch_id::uuid
  AND input_fetch_expires_at > now() AND input_lease_expires_at > now();

-- name: ReleaseInputFetch :execrows
UPDATE agent_run SET input_fetch_id = NULL, input_fetch_expires_at = NULL
WHERE id = @id AND workspace_id = @workspace_id AND input_fetch_id = @fetch_id::uuid;

-- name: ClearInputFetch :exec
UPDATE agent_run SET input_fetch_id = NULL, input_fetch_expires_at = NULL,
  input_lease_expires_at = NULL, input_outcomes = '{}'
WHERE id = @id AND workspace_id = @workspace_id;
