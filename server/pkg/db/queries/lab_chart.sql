-- The shared laboratory's Team role of each of its agents.
-- name: LabChartRoles :many
SELECT agent_id, member FROM team_agent WHERE workspace_id = @workspace_id AND user_id IS NULL;

-- Each agent's runtime, as a machine and CLI.
-- name: LabChartRuntimes :many
SELECT id, COALESCE(custom_name, NULLIF(device_name, ''), 'machine')::text AS machine, harness FROM runtime WHERE workspace_id = @workspace_id;

-- The agents' runs in flight, with their studies.
-- name: LabChartActiveRuns :many
SELECT r.agent_id, r.card_id, c.title, r.runtime_id, r.harness, COALESCE(r.started_at, r.dispatched_at, r.created_at)::timestamptz AS since
FROM agent_run r JOIN card c ON c.workspace_id = r.workspace_id AND c.id = r.card_id
WHERE r.workspace_id = @workspace_id AND r.kind = 'role' AND r.agent_id IS NOT NULL AND r.status IN ('dispatched', 'running')
ORDER BY since, r.id;

-- The open agent decisions with the agent whose role run last worked the
-- study, and whether the viewer may answer each.
-- name: LabChartDecisions :many
SELECT i.card_id, i.created_at, c.title,
  (SELECT r.agent_id FROM agent_run r WHERE r.workspace_id = i.workspace_id AND r.card_id = i.card_id AND r.kind = 'role' AND r.agent_id IS NOT NULL
    ORDER BY r.created_at DESC, r.id DESC LIMIT 1)::uuid AS agent_id,
  ((jsonb_array_length(i.options) > 0 OR i.question_set IS NOT NULL) AND (@is_admin::bool OR c.owner_id = @viewer::uuid))::bool AS may_answer
FROM inbox_item i JOIN card c ON c.workspace_id = i.workspace_id AND c.id = i.card_id
WHERE i.workspace_id = @workspace_id AND i.kind = 'agent_decision' AND i.status = 'open' AND i.resolved_at IS NULL AND i.archived_at IS NULL;

-- Each agent's last finished run, one index probe per agent.
-- name: LabChartLastFinished :many
SELECT a.id AS agent_id, l.completed_at::timestamptz AS at FROM agent a
JOIN LATERAL (SELECT r.completed_at FROM agent_run r WHERE r.workspace_id = a.workspace_id AND r.agent_id = a.id
  AND r.kind = 'role' AND r.status = 'completed' AND r.completed_at IS NOT NULL ORDER BY r.completed_at DESC LIMIT 1) l ON true
WHERE a.workspace_id = @workspace_id AND a.stage <> '' AND a.deleted_at IS NULL AND a.archived_at IS NULL;

-- The newest of run started, run finished and decision opened, among the
-- agents the viewer may see (and decisions the viewer may answer).
-- name: LabChartTicker :one
SELECT at, event, agent_name, stage, title FROM (
  (SELECT r.started_at AS at, 'started' AS event, a.name AS agent_name, r.board_column AS stage, c.title
  FROM agent_run r JOIN agent a ON a.workspace_id = r.workspace_id AND a.id = r.agent_id
    JOIN card c ON c.workspace_id = r.workspace_id AND c.id = r.card_id
  WHERE r.workspace_id = @workspace_id AND r.kind = 'role' AND r.started_at IS NOT NULL
    AND (a.visibility = 'workspace' OR a.owner_id = @viewer::uuid OR @is_admin::bool)
  ORDER BY r.started_at DESC LIMIT 1)
  UNION ALL
  (SELECT r.completed_at, 'finished', a.name, r.board_column, c.title
  FROM agent_run r JOIN agent a ON a.workspace_id = r.workspace_id AND a.id = r.agent_id
    JOIN card c ON c.workspace_id = r.workspace_id AND c.id = r.card_id
  WHERE r.workspace_id = @workspace_id AND r.kind = 'role' AND r.status = 'completed' AND r.completed_at IS NOT NULL
    AND (a.visibility = 'workspace' OR a.owner_id = @viewer::uuid OR @is_admin::bool)
  ORDER BY r.completed_at DESC LIMIT 1)
  UNION ALL
  (SELECT i.created_at, 'waiting', a.name, c.board_column, c.title
  FROM inbox_item i JOIN card c ON c.workspace_id = i.workspace_id AND c.id = i.card_id
    JOIN LATERAL (SELECT r.agent_id, r.board_column FROM agent_run r WHERE r.workspace_id = i.workspace_id AND r.card_id = i.card_id
      AND r.kind = 'role' AND r.agent_id IS NOT NULL ORDER BY r.created_at DESC, r.id DESC LIMIT 1) lr ON true
    JOIN agent a ON a.workspace_id = i.workspace_id AND a.id = lr.agent_id
  WHERE i.workspace_id = @workspace_id AND i.kind = 'agent_decision' AND i.status = 'open' AND i.resolved_at IS NULL AND i.archived_at IS NULL
    AND (jsonb_array_length(i.options) > 0 OR i.question_set IS NOT NULL) AND (@is_admin::bool OR c.owner_id = @viewer::uuid)
    AND (a.visibility = 'workspace' OR a.owner_id = @viewer::uuid OR @is_admin::bool)
  ORDER BY i.created_at DESC LIMIT 1)
) e ORDER BY at DESC LIMIT 1;

-- Each member's open answerable decisions, as their own Decisions view counts them.
-- name: LabChartPeople :many
SELECT m.user_id, m.role, u.name,
  (SELECT count(*) FROM inbox_item i LEFT JOIN card c ON c.id = i.card_id
    WHERE i.workspace_id = m.workspace_id AND (i.card_id IS NOT NULL OR i.user_id = m.user_id) AND i.kind <> 'manual_decision' AND i.status = 'open' AND i.resolved_at IS NULL AND i.archived_at IS NULL
      AND (jsonb_array_length(i.options) > 0 OR i.question_set IS NOT NULL)
      AND (m.role IN ('owner', 'admin') OR c.owner_id = m.user_id OR (i.card_id IS NULL AND i.user_id = m.user_id)))::bigint AS open_decisions
FROM member m JOIN "user" u ON u.id = m.user_id WHERE m.workspace_id = @workspace_id ORDER BY m.created_at;
