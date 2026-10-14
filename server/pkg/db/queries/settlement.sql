-- name: LockRuntime :one
SELECT * FROM runtime WHERE id = @id AND workspace_id = @workspace_id FOR UPDATE;

-- name: CountRuntimeActive :one
SELECT count(*) FROM agent_run WHERE workspace_id = @workspace_id AND runtime_id = @runtime_id
 AND status IN ('dispatched', 'running');

-- name: GetAttemptReceipt :one
SELECT * FROM agent_run_receipt WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id AND attempt = @attempt;

-- name: InsertAttemptReceipt :exec
INSERT INTO agent_run_receipt (workspace_id, agent_run_id, attempt, runtime_id, result)
VALUES (@workspace_id, @agent_run_id, @attempt, @runtime_id, @result);

-- name: InsertSettlement :exec
INSERT INTO agent_run_settlement (workspace_id, agent_run_id, result)
VALUES (@workspace_id, @agent_run_id, @result);

-- name: LockPendingSettlement :one
SELECT * FROM agent_run_settlement WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id
 AND delivered_at IS NULL FOR UPDATE SKIP LOCKED;

-- name: AcknowledgeSettlement :exec
UPDATE agent_run_settlement SET delivered_at = now() WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id;

-- name: PendingSettlements :many
SELECT agent_run_id::uuid AS agent_run_id, workspace_id::uuid AS workspace_id FROM pending_agent_run_settlements();

-- name: SweepCandidates :many
SELECT agent_run_id::uuid AS agent_run_id, workspace_id::uuid AS workspace_id FROM agent_run_sweep_candidates();
