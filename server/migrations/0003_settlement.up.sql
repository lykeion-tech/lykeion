-- Attempt receipts also cover reports with no usage. Their result remains stable
-- when the run is requeued and another attempt starts on the same runtime.
CREATE TABLE agent_run_receipt (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_run_id uuid NOT NULL,
  attempt int NOT NULL CHECK (attempt > 0),
  runtime_id uuid,
  result jsonb NOT NULL,
  PRIMARY KEY (agent_run_id, attempt),
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);

CREATE TABLE agent_run_settlement (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_run_id uuid PRIMARY KEY,
  result jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  delivered_at timestamptz,
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);
CREATE INDEX agent_run_settlement_pending ON agent_run_settlement (created_at) WHERE delivered_at IS NULL;

ALTER TABLE agent_run_receipt ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_run_receipt USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE agent_run_settlement ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_run_settlement USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_run_receipt, agent_run_settlement TO lykeion_app;

-- These helpers discover tenant identities for maintenance. Mutations and event
-- writes then run under the corresponding workspace settings in one transaction.
CREATE FUNCTION agent_run_sweep_candidates()
RETURNS TABLE (agent_run_id uuid, workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
 SELECT t.id, t.workspace_id FROM agent_run t
 WHERE (t.status = 'dispatched' AND t.prepare_lease_expires_at <= now())
    OR (t.status = 'running' AND (t.runtime_id IS NULL OR EXISTS (
       SELECT 1 FROM runtime r WHERE r.id = t.runtime_id AND r.status = 'offline')))
 ORDER BY t.workspace_id, t.id
$$;
CREATE FUNCTION pending_agent_run_settlements()
RETURNS TABLE (agent_run_id uuid, workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
 SELECT n.agent_run_id, n.workspace_id FROM agent_run_settlement n
 WHERE n.delivered_at IS NULL ORDER BY n.created_at, n.agent_run_id
$$;
REVOKE ALL ON FUNCTION agent_run_sweep_candidates(), pending_agent_run_settlements() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_run_sweep_candidates(), pending_agent_run_settlements() TO lykeion_app;
