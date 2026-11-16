-- Preserve the earliest proven start across retries.
ALTER TABLE agent_run ADD COLUMN first_started_at timestamptz;
UPDATE agent_run SET first_started_at = LEAST(started_at, (
  SELECT min(a.started_at) FROM agent_run_machine_attempt a
  WHERE a.workspace_id = agent_run.workspace_id AND a.agent_run_id = agent_run.id
));
UPDATE agent_run r SET first_started_at = LEAST(r.first_started_at, e.started_at)
FROM (
  SELECT workspace_id, payload->>'agent_run_id' AS run_id, min(created_at) AS started_at
  FROM event WHERE type = 'agent_run.started' GROUP BY workspace_id, payload->>'agent_run_id'
) e WHERE e.workspace_id = r.workspace_id AND e.run_id = r.id::text;

-- Historical finished runs may predate recorded attempt start times.
ALTER TABLE agent_run ADD COLUMN legacy_unverified_started boolean NOT NULL DEFAULT false;
UPDATE agent_run SET legacy_unverified_started = true
WHERE status = 'completed' AND first_started_at IS NULL AND completed_at IS NOT NULL;

-- Older review records predate the recorded Jev lifecycle.
ALTER TABLE record ADD COLUMN legacy_review_timeline boolean NOT NULL DEFAULT false;
UPDATE record SET legacy_review_timeline = true WHERE kind = 'review';

-- The rating feature is removed; existing ratings are intentionally deleted.
DROP TABLE team_rating;
