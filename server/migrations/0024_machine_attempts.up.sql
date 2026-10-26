-- Attribution belongs to an accepted attempt, never a run's current runtime.
-- No runtime/member FK: deleting a CLI or leaving must not erase history.
CREATE TABLE agent_run_machine_attempt (
 workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
 agent_run_id uuid NOT NULL,
 attempt int NOT NULL CHECK (attempt > 0),
 runtime_id uuid NOT NULL,
 daemon_id text NOT NULL,
 owner_id uuid NOT NULL,
 responsible_user_id uuid NOT NULL,
 accepted_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 PRIMARY KEY (agent_run_id, attempt),
 FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);
CREATE INDEX agent_run_machine_attempt_machine ON agent_run_machine_attempt (workspace_id, daemon_id, owner_id, accepted_at DESC);
ALTER TABLE agent_run_machine_attempt ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_run_machine_attempt USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_run_machine_attempt TO lykeion_app;

-- Claim refusals also have receipts: require start or usage evidence. Unknown
-- deleted runtime identities cannot be recovered; never join by display name or
-- borrow a later attempt's current runtime. Registration cannot change an owner.
INSERT INTO agent_run_machine_attempt (workspace_id, agent_run_id, attempt, runtime_id, daemon_id, owner_id, responsible_user_id, accepted_at, started_at)
SELECT receipt.workspace_id, receipt.agent_run_id, receipt.attempt, rt.id, rt.daemon_id, rt.owner_id,
 (receipt.result->>'responsible_user_id')::uuid,
 COALESCE((receipt.result->>'dispatched_at')::timestamptz, (receipt.result->>'started_at')::timestamptz, r.created_at),
 (receipt.result->>'started_at')::timestamptz
FROM agent_run_receipt receipt
JOIN agent_run r ON r.id = receipt.agent_run_id AND r.workspace_id = receipt.workspace_id
JOIN runtime rt ON rt.id = receipt.runtime_id AND rt.workspace_id = receipt.workspace_id
WHERE receipt.result->>'responsible_user_id' IS NOT NULL AND (receipt.result->>'started_at' IS NOT NULL OR EXISTS (
 SELECT 1 FROM agent_run_usage u WHERE u.workspace_id = receipt.workspace_id
 AND u.agent_run_id = receipt.agent_run_id AND u.attempt = receipt.attempt
));

-- A current in-flight attempt with its issued token is accepted provenance.
INSERT INTO agent_run_machine_attempt (workspace_id, agent_run_id, attempt, runtime_id, daemon_id, owner_id, responsible_user_id, accepted_at, started_at)
SELECT r.workspace_id, r.id, r.attempt, rt.id, rt.daemon_id, rt.owner_id, r.responsible_user_id,
 COALESCE(r.dispatched_at, r.started_at, r.created_at), r.started_at
FROM agent_run r JOIN runtime rt ON rt.id = r.runtime_id AND rt.workspace_id = r.workspace_id
WHERE r.status IN ('dispatched', 'running') AND r.attempt > 0 AND EXISTS (
 SELECT 1 FROM agent_run_token tok WHERE tok.workspace_id = r.workspace_id AND tok.agent_run_id = r.id AND tok.attempt = r.attempt
)
ON CONFLICT (agent_run_id, attempt) DO NOTHING;
