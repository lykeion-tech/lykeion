-- The one runtime a laboratory agent's runs go to. Agents people make are
-- never bound: their runs follow the study's owner and chosen machine.
ALTER TABLE agent ADD COLUMN runtime_id uuid,
  ADD CONSTRAINT agent_runtime_fkey FOREIGN KEY (workspace_id, runtime_id)
    REFERENCES runtime (workspace_id, id) ON DELETE SET NULL (runtime_id),
  ADD CONSTRAINT agent_runtime_laboratory CHECK (runtime_id IS NULL OR laboratory <> '');

-- The laboratory agent whose binding decides where a study's run goes: a
-- role run's own agent, or, for a rerun, the agent leading its round. No row
-- when that agent is not a laboratory agent.
CREATE FUNCTION agent_run_laboratory(ws uuid, run uuid)
RETURNS TABLE (id uuid, owner_id uuid, runtime_id uuid, name text) LANGUAGE sql STABLE AS $$
  SELECT a.id, a.owner_id, a.runtime_id, a.name FROM agent_run t
  JOIN agent a ON a.workspace_id = t.workspace_id AND a.laboratory <> '' AND a.id = CASE WHEN t.kind = 'rerun' THEN
    (SELECT l.agent_id FROM agent_run l WHERE l.workspace_id = t.workspace_id AND l.card_id = t.card_id
       AND l.board_column = t.board_column AND l.round = t.round AND l.kind = 'role' AND l.role IN ('leader', 'agent')
     ORDER BY l.created_at DESC, l.id DESC LIMIT 1)
    ELSE t.agent_id END
  WHERE t.workspace_id = ws AND t.id = run AND t.kind IN ('role', 'rerun')
$$;

CREATE OR REPLACE FUNCTION agent_run_waiting(ws uuid, run uuid)
RETURNS text LANGUAGE sql STABLE AS $$
  WITH t AS (
    SELECT * FROM agent_run WHERE workspace_id = ws AND id = run AND status = 'queued'
  ), last AS (
    SELECT r.* FROM t
    JOIN agent_run_receipt rc ON rc.workspace_id = t.workspace_id AND rc.agent_run_id = t.id AND rc.attempt = t.attempt
    JOIN runtime r ON r.workspace_id = rc.workspace_id AND r.id = rc.runtime_id
  ), candidates AS (
    SELECT r.* FROM t
    JOIN card c ON c.workspace_id = t.workspace_id AND c.id = t.card_id
    JOIN runtime r ON r.workspace_id = t.workspace_id AND r.harness = t.harness
    LEFT JOIN agent_run_laboratory(t.workspace_id, t.id) la ON true
    WHERE NOT EXISTS (SELECT 1 FROM last)
      AND t.kind = 'role' AND t.responsible_user_id = c.owner_id AND c.flag = '' AND c.ending = ''
      AND (t.responsible_user_id = r.owner_id OR r.os = 'linux')
      AND ((la.id IS NOT NULL AND r.id = la.runtime_id AND (r.owner_id = la.owner_id OR (r.os = 'linux' AND (r.visibility = 'workspace'
          OR EXISTS (SELECT 1 FROM runtime_share s WHERE s.workspace_id = r.workspace_id AND s.runtime_id = r.id AND s.user_id = la.owner_id)))))
        OR (la.id IS NULL AND ((c.machine IS NULL AND c.owner_id = r.owner_id)
        OR (c.machine = r.daemon_id AND (r.owner_id = c.owner_id OR (r.os = 'linux' AND (r.visibility = 'workspace'
          OR EXISTS (SELECT 1 FROM runtime_share s WHERE s.workspace_id = r.workspace_id AND s.runtime_id = r.id AND s.user_id = c.owner_id))))))))
  ), machines AS (
    SELECT * FROM last UNION ALL SELECT * FROM candidates
  ), why AS (
    SELECT m.created_at, COALESCE(m.custom_name, m.device_name) AS name,
      (SELECT i.text FROM inbox_item i WHERE i.workspace_id = m.workspace_id AND i.runtime_id = m.id
        AND i.kind = 'waiting_for_machine' AND i.resolved_at IS NULL ORDER BY i.created_at LIMIT 1) AS wait,
      m.status <> 'online' AS offline
    FROM machines m
    WHERE NOT EXISTS (SELECT 1 FROM machines a WHERE a.status = 'online' AND a.unhealthy_reason IS NULL)
  )
  SELECT COALESCE(
    (SELECT 'Waiting for ' || name || ': ' || COALESCE(wait, 'it is offline.')
     FROM why WHERE wait IS NOT NULL OR offline ORDER BY wait IS NULL, created_at LIMIT 1),
    (SELECT 'Waiting for a machine: ' || la.name || CASE WHEN la.runtime_id IS NULL THEN ' is not bound to one yet.'
       ELSE ' is bound to a machine the workspace owner may no longer use.' END || ' The workspace owner chooses its machine in Agents.'
     FROM t, agent_run_laboratory(t.workspace_id, t.id) la WHERE t.kind = 'role' AND NOT EXISTS (SELECT 1 FROM machines)),
    (SELECT 'Waiting for ' || CASE t.harness WHEN 'claude' THEN 'Claude Code' ELSE 'Codex' END || ': connect this CLI on the selected machine.'
     FROM t JOIN card c ON c.workspace_id = t.workspace_id AND c.id = t.card_id
     WHERE c.study_cli IS NOT NULL AND c.auditor_cli IS NOT NULL AND NOT EXISTS (SELECT 1 FROM machines)
       AND NOT EXISTS (SELECT 1 FROM agent_run_laboratory(t.workspace_id, t.id)))
  )
$$;
