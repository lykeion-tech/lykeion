-- name: LockRuntime :one
SELECT * FROM runtime WHERE id = @id AND workspace_id = @workspace_id FOR UPDATE;

-- The runs holding a runtime's slots. A leader among them gives its slot up
-- while its member works; the claim checks that with InFlightMemberRun.
-- name: RuntimeActiveRuns :many
SELECT card_id, board_column, round, role FROM agent_run WHERE workspace_id = @workspace_id AND runtime_id = @runtime_id
 AND status IN ('dispatched', 'running') AND kind IN ('role','autoresearch','rerun');

-- name: GetAttemptReceipt :one
SELECT * FROM agent_run_receipt WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id AND attempt = @attempt;

-- name: InsertAttemptReceipt :exec
INSERT INTO agent_run_receipt (workspace_id, agent_run_id, attempt, runtime_id, result)
VALUES (@workspace_id, @agent_run_id, @attempt, @runtime_id, @result);

-- name: InsertSettlement :exec
INSERT INTO agent_run_settlement (workspace_id, agent_run_id, result)
VALUES (@workspace_id, @agent_run_id, @result);

-- name: ClaimSettlement :one
UPDATE agent_run_settlement SET delivery_id = @delivery_id, lease_expires_at = now() + @lease::interval
WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id AND delivered_at IS NULL
 AND (lease_expires_at IS NULL OR lease_expires_at <= now())
RETURNING *;

-- name: AcknowledgeSettlement :execrows
UPDATE agent_run_settlement SET delivered_at = now(), delivery_id = NULL, lease_expires_at = NULL
WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id
 AND delivered_at IS NULL AND delivery_id = @delivery_id AND lease_expires_at > now();

-- name: ReleaseSettlement :execrows
UPDATE agent_run_settlement SET delivery_id = NULL, lease_expires_at = NULL
WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id
 AND delivered_at IS NULL AND delivery_id = @delivery_id;

-- name: PendingSettlements :many
SELECT agent_run_id::uuid AS agent_run_id, workspace_id::uuid AS workspace_id FROM pending_agent_run_settlements();

-- name: SweepCandidates :many
SELECT agent_run_id::uuid AS agent_run_id, workspace_id::uuid AS workspace_id FROM agent_run_sweep_candidates();
