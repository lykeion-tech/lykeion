-- name: RenewInputLease :exec
UPDATE agent_run SET input_lease_expires_at = now() + @lease::interval
WHERE id = @id AND workspace_id = @workspace_id;

-- Takes the fetch claim when no live claim holds it; one row means taken.
-- name: ClaimInputFetch :execrows
UPDATE agent_run SET input_fetch_expires_at = now() + @lease::interval
WHERE id = @id AND workspace_id = @workspace_id
  AND (input_fetch_expires_at IS NULL OR input_fetch_expires_at <= now());

-- Renews a fetch's claim only while a daemon still asks; no row means the
-- input lease lapsed and the fetch stops.
-- name: RenewInputFetch :execrows
UPDATE agent_run SET input_fetch_expires_at = now() + @lease::interval
WHERE id = @id AND workspace_id = @workspace_id AND input_lease_expires_at > now();

-- name: RecordInputOutcome :exec
UPDATE agent_run SET input_outcomes = input_outcomes || @outcome::jsonb
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ReleaseInputFetch :exec
UPDATE agent_run SET input_fetch_expires_at = NULL WHERE id = @id AND workspace_id = @workspace_id;

-- name: ClearInputFetch :exec
UPDATE agent_run SET input_lease_expires_at = NULL, input_outcomes = '{}'
WHERE id = @id AND workspace_id = @workspace_id;
