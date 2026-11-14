-- The agent a run was started as, and the part of its prompt that is the
-- run's own assignment, for the study page. Runs started before this have
-- neither; the page then names them by role.
ALTER TABLE agent_run
  ADD COLUMN agent_id uuid,
  ADD COLUMN assignment text NOT NULL DEFAULT '',
  ADD CONSTRAINT agent_run_agent_fkey FOREIGN KEY (workspace_id, agent_id)
    REFERENCES agent (workspace_id, id) ON DELETE SET NULL (agent_id);

-- What a daemon reports about the machine a runtime runs on. Empty means it
-- was not reported.
ALTER TABLE runtime
  ADD COLUMN os text NOT NULL DEFAULT '' CHECK (char_length(os) <= 32),
  ADD COLUMN arch text NOT NULL DEFAULT '' CHECK (char_length(arch) <= 32),
  ADD COLUMN kernel text NOT NULL DEFAULT '' CHECK (char_length(kernel) <= 64),
  ADD COLUMN language text NOT NULL DEFAULT '' CHECK (char_length(language) <= 64);

-- The latest text an attempt recorded, at most 600 characters, for a run's
-- preview. joined adds the text records that directly precede the latest
-- one, for harnesses that record text in pieces.
CREATE FUNCTION agent_run_excerpt(ws uuid, run uuid, try int, joined boolean)
RETURNS text LANGUAGE sql STABLE AS $$
  WITH latest AS (
    SELECT max(seq) AS seq FROM agent_run_message
    WHERE workspace_id = ws AND agent_run_id = run AND attempt = try
      AND type = 'text' AND jsonb_typeof(content) = 'string'
  ), since AS (
    SELECT COALESCE(max(m.seq), -1) AS seq FROM agent_run_message m, latest
    WHERE m.workspace_id = ws AND m.agent_run_id = run AND m.attempt = try
      AND m.seq < latest.seq AND NOT (m.type = 'text' AND jsonb_typeof(m.content) = 'string')
  )
  SELECT left(string_agg(m.content #>> '{}', '' ORDER BY m.seq), 600)
  FROM agent_run_message m, latest, since
  WHERE m.workspace_id = ws AND m.agent_run_id = run AND m.attempt = try
    AND m.type = 'text' AND jsonb_typeof(m.content) = 'string'
    AND (m.seq = latest.seq OR (joined AND m.seq > since.seq AND m.seq < latest.seq))
$$;

REVOKE ALL ON FUNCTION agent_run_excerpt(uuid, uuid, int, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_run_excerpt(uuid, uuid, int, boolean) TO lykeion_app;
