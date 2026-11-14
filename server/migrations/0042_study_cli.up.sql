ALTER TABLE card ADD COLUMN study_cli text CHECK (study_cli IN ('claude', 'codex')),
  ADD COLUMN auditor_cli text CHECK (auditor_cli IN ('claude', 'codex')),
  ADD CONSTRAINT card_distinct_auditor_cli CHECK (study_cli IS NULL OR auditor_cli IS NULL OR study_cli <> auditor_cli);

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
    WHERE NOT EXISTS (SELECT 1 FROM last)
      AND t.kind = 'role' AND t.responsible_user_id = c.owner_id AND c.flag = '' AND c.ending = ''
      AND (t.responsible_user_id = r.owner_id OR r.os = 'linux')
      AND ((c.machine IS NULL AND c.owner_id = r.owner_id)
        OR (c.machine = r.daemon_id AND (r.owner_id = c.owner_id OR (r.os = 'linux' AND (r.visibility = 'workspace'
          OR EXISTS (SELECT 1 FROM runtime_share s WHERE s.workspace_id = r.workspace_id AND s.runtime_id = r.id AND s.user_id = c.owner_id))))))
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
    (SELECT 'Waiting for ' || CASE t.harness WHEN 'claude' THEN 'Claude Code' ELSE 'Codex' END || ': connect this CLI on the selected machine.'
     FROM t JOIN card c ON c.workspace_id = t.workspace_id AND c.id = t.card_id
     WHERE c.study_cli IS NOT NULL AND c.auditor_cli IS NOT NULL AND NOT EXISTS (SELECT 1 FROM machines))
  )
$$;

