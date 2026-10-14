-- A delivery claim is committed before invoking the consumer, so a consumer can
-- open its own transaction even when the application pool has one connection.
ALTER TABLE agent_run_settlement
  ADD COLUMN delivery_id uuid,
  ADD COLUMN lease_expires_at timestamptz,
  ADD CONSTRAINT settlement_delivery_lease CHECK ((delivery_id IS NULL) = (lease_expires_at IS NULL));

CREATE OR REPLACE FUNCTION pending_agent_run_settlements()
RETURNS TABLE (agent_run_id uuid, workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
 SELECT n.agent_run_id, n.workspace_id FROM agent_run_settlement n
 WHERE n.delivered_at IS NULL AND (n.lease_expires_at IS NULL OR n.lease_expires_at <= now())
 ORDER BY n.created_at, n.agent_run_id
$$;
