-- name: AnalyticsRuns :many
SELECT r.*, COALESCE(a.name, '')::text AS agent_name
FROM agent_run r
JOIN card c ON c.id = r.card_id AND c.workspace_id = r.workspace_id
LEFT JOIN agent a ON a.id = r.agent_id AND a.workspace_id = r.workspace_id
WHERE r.workspace_id = @workspace_id
  AND (@scope::text = 'all'
    OR (@scope::text = 'standalone' AND c.research_id IS NULL)
    OR c.research_id = sqlc.narg(research_id)::uuid)
ORDER BY r.id;

-- name: AnalyticsUsage :many
SELECT u.* FROM agent_run_usage u
JOIN agent_run r ON r.id = u.agent_run_id AND r.workspace_id = u.workspace_id
JOIN card c ON c.id = r.card_id AND c.workspace_id = r.workspace_id
WHERE u.workspace_id = @workspace_id
  AND (@scope::text = 'all'
    OR (@scope::text = 'standalone' AND c.research_id IS NULL)
    OR c.research_id = sqlc.narg(research_id)::uuid)
ORDER BY u.agent_run_id, u.attempt, u.model;

-- name: AnalyticsEvents :many
SELECT e.* FROM event e
JOIN card c ON c.id = e.card_id AND c.workspace_id = e.workspace_id
WHERE e.workspace_id = @workspace_id AND e.type IN (
 'agent_run.dispatched', 'agent_run.started', 'agent_run.completed',
 'agent_run.failed', 'agent_run.cancelled', 'agent_run.queued', 'agent_run.requeued')
 AND (@scope::text = 'all'
    OR (@scope::text = 'standalone' AND c.research_id IS NULL)
    OR c.research_id = sqlc.narg(research_id)::uuid)
ORDER BY e.id;
