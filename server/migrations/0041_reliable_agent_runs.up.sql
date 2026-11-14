ALTER TABLE agent_run DROP CONSTRAINT agent_run_failure_reason_check,
  ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline','timeout','provider_network','context_exhausted','agent_error','runtime_access_denied','coresearch_access_revoked',
    'sign_in','cli_missing','cli_too_old','door','cli_broken','sandbox','fence_breach','hand_in_invalid'));

ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check,
  ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'released', 'autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done', 'waiting_for_machine', 'needs_decision', 'open_doubts'));

ALTER TABLE inbox_item ADD COLUMN runtime_id uuid REFERENCES runtime(id) ON DELETE CASCADE, ADD COLUMN cause text;
-- One open machine-wait item per machine and cause, however many runs wait on it.
CREATE UNIQUE INDEX inbox_item_open_machine_wait ON inbox_item (workspace_id, runtime_id, cause)
  WHERE kind = 'waiting_for_machine' AND resolved_at IS NULL;
-- One open decision item per study and cause.
CREATE UNIQUE INDEX inbox_item_open_decision ON inbox_item (workspace_id, card_id, cause)
  WHERE kind = 'needs_decision' AND resolved_at IS NULL;

ALTER TABLE agent_run ADD COLUMN retry_after timestamptz, ADD COLUMN infra_failures int NOT NULL DEFAULT 0;
-- infra_streak counts the runtime's machine failures since its last success.
ALTER TABLE runtime ADD COLUMN unhealthy_reason text, ADD COLUMN infra_streak int NOT NULL DEFAULT 0;
ALTER TABLE agent_run_usage ADD COLUMN estimated boolean NOT NULL DEFAULT false;

-- Why a queued run waits on a machine, as the study shows it: "Waiting for
-- <machine>: <why>", or NULL when it does not. The machines are the one its
-- last attempt ran on or, before any attempt, every runtime ClaimAgentRun
-- would give it (a Linux daemon claims with guest isolation). It waits when
-- none of them is online and healthy; the why is the machine's open wait, or
-- that it is offline.
CREATE FUNCTION agent_run_waiting(ws uuid, run uuid)
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
  SELECT 'Waiting for ' || name || ': ' || COALESCE(wait, 'it is offline.')
  FROM why WHERE wait IS NOT NULL OR offline
  ORDER BY wait IS NULL, created_at LIMIT 1
$$;

REVOKE ALL ON FUNCTION agent_run_waiting(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_run_waiting(uuid, uuid) TO lykeion_app;
