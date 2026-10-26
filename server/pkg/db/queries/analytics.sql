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

-- name: MachineOwnedRuntime :one
-- Authorization precedes either usage query, including for an empty machine.
SELECT id FROM runtime
WHERE workspace_id = @workspace_id AND daemon_id = @daemon_id AND owner_id = @owner_id
LIMIT 1;

-- name: MachineGuestRuns :many
WITH machine_runs AS (
 SELECT DISTINCT ON (a.agent_run_id) a.agent_run_id, a.workspace_id, a.responsible_user_id,
        a.started_at, COALESCE(a.started_at, a.accepted_at) AS last_at
 FROM agent_run_machine_attempt a
 WHERE a.workspace_id = @workspace_id AND a.daemon_id = @daemon_id
   AND a.owner_id = @owner_id AND a.responsible_user_id <> @owner_id
 ORDER BY a.agent_run_id, COALESCE(a.started_at, a.accepted_at) DESC, a.attempt DESC
), recent AS (
 SELECT r.id, r.workspace_id, r.card_id, a.responsible_user_id, r.harness,
        a.started_at, r.status, a.last_at
 FROM machine_runs a JOIN agent_run r ON r.id = a.agent_run_id AND r.workspace_id = a.workspace_id
 ORDER BY a.last_at DESC, r.id DESC
 LIMIT 200
)
SELECT r.id AS run_id, COALESCE(NULLIF(person.name, ''), person.email)::text AS colleague,
       COALESCE(c.title, '')::text AS study_title, r.harness, r.started_at, r.status,
       COALESCE(usage.input_tokens, 0)::bigint AS input_tokens,
       COALESCE(usage.output_tokens, 0)::bigint AS output_tokens,
       COALESCE(usage.cost_usd, 0)::double precision AS cost_usd
FROM recent r
JOIN "user" person ON person.id = r.responsible_user_id
LEFT JOIN card c ON c.id = r.card_id AND c.workspace_id = r.workspace_id
LEFT JOIN LATERAL (
 SELECT sum(u.input_tokens + u.cache_read_tokens + u.cache_write_tokens)::bigint AS input_tokens,
        sum(u.output_tokens)::bigint AS output_tokens, sum(u.cost_usd)::double precision AS cost_usd
 FROM agent_run_machine_attempt a
 JOIN agent_run_usage u ON u.agent_run_id = a.agent_run_id AND u.workspace_id = a.workspace_id AND u.attempt = a.attempt
 WHERE a.agent_run_id = r.id AND a.workspace_id = r.workspace_id
   AND a.daemon_id = @daemon_id AND a.owner_id = @owner_id AND a.responsible_user_id <> @owner_id
) usage ON true
ORDER BY r.last_at DESC, r.id DESC;

-- name: MachineGuestUsage :many
-- Windows follow usage receipt time, independent of run age and the display limit.
SELECT a.responsible_user_id,
       COALESCE(NULLIF(person.name, ''), person.email)::text AS colleague,
       COALESCE(sum(u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens)
         FILTER (WHERE u.created_at >= sqlc.arg(as_of)::timestamptz - interval '7 days'), 0)::bigint AS tokens_7_days,
       COALESCE(sum(u.cost_usd)
         FILTER (WHERE u.created_at >= sqlc.arg(as_of)::timestamptz - interval '7 days'), 0)::double precision AS cost_7_days,
       COALESCE(sum(u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens), 0)::bigint AS tokens_30_days,
       COALESCE(sum(u.cost_usd), 0)::double precision AS cost_30_days
FROM agent_run_machine_attempt a
JOIN "user" person ON person.id = a.responsible_user_id
LEFT JOIN agent_run_usage u ON u.agent_run_id = a.agent_run_id AND u.workspace_id = a.workspace_id AND u.attempt = a.attempt
  AND u.created_at >= sqlc.arg(as_of)::timestamptz - interval '30 days' AND u.created_at < sqlc.arg(as_of)::timestamptz
WHERE a.workspace_id = @workspace_id AND a.daemon_id = @daemon_id
  AND a.owner_id = @owner_id AND a.responsible_user_id <> @owner_id
GROUP BY a.responsible_user_id, person.name, person.email
ORDER BY colleague, a.responsible_user_id;
