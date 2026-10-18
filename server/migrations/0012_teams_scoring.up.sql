-- Studies and Teams of the earlier board design cannot be read by the new
-- one, so they go, with everything that cascades from them.
DELETE FROM inbox_item WHERE kind IN ('out_of_budget', 'revised', 'reopened', 'publish_request');
DELETE FROM card;
DELETE FROM field_pack;
DELETE FROM autoresearch WHERE execution_mode = 'watch_released';

ALTER TABLE card
  DROP COLUMN budget_usd,
  DROP COLUMN start_kind,
  DROP COLUMN cap,
  DROP COLUMN route_backs,
  DROP COLUMN reopens,
  DROP COLUMN stale,
  DROP COLUMN rerun,
  ADD COLUMN scope_from text NOT NULL DEFAULT 'grounding'
    CHECK (scope_from IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  ADD COLUMN scope_to text NOT NULL DEFAULT 'reporting'
    CHECK (scope_to IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  ADD COLUMN redos int NOT NULL DEFAULT 0,
  ADD CONSTRAINT card_scope_order CHECK (
    array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_from)
    <= array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_to)),
  ADD CONSTRAINT card_follows_end_to_end CHECK (
    cardinality(follows_from) = 0 OR (scope_from = 'grounding' AND scope_to = 'reporting')),
  ADD CONSTRAINT card_state_check CHECK (state IN ('working', 'done', 'blocked', 'stopped')),
  DROP CONSTRAINT card_block_kind_check,
  ADD CONSTRAINT card_block_kind_check CHECK (block_kind IN ('', 'low_score')),
  DROP CONSTRAINT card_assignee_tuple,
  ADD CONSTRAINT card_assignee_tuple CHECK (
    (assignee_type IS NULL AND assignee_id IS NULL) OR
    (assignee_type IS NOT NULL AND assignee_type IN ('member', 'agent') AND assignee_id IS NOT NULL) OR
    (assignee_type IS NOT NULL AND assignee_type = 'team'));

-- A team assignee_id is the agent for columns no Team covers, so it is
-- checked as an agent.
CREATE OR REPLACE FUNCTION check_card_assignee() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.assignee_type = 'member' AND NOT EXISTS (
    SELECT 1 FROM member WHERE workspace_id = NEW.workspace_id AND user_id = NEW.assignee_id
  ) THEN
    RAISE EXCEPTION 'assignee is not a workspace member' USING ERRCODE = '23503';
  ELSIF NEW.assignee_type IN ('agent', 'team') AND NEW.assignee_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM agent WHERE workspace_id = NEW.workspace_id AND id = NEW.assignee_id
  ) THEN
    RAISE EXCEPTION 'assignee is not a workspace agent' USING ERRCODE = '23503';
  END IF;
  RETURN NEW;
END $$;

ALTER TABLE field_pack
  ADD COLUMN domain text NOT NULL DEFAULT '',
  ADD COLUMN stage text NOT NULL DEFAULT ''
    CHECK (stage IN ('', 'grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'));

-- The Team that works each column of a study.
CREATE TABLE card_team (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  board_column text NOT NULL CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  pack_id uuid NOT NULL,
  PRIMARY KEY (card_id, board_column),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, pack_id) REFERENCES field_pack (workspace_id, id)
);

-- The agents created for a researcher who added a Team, by member name.
CREATE TABLE team_agent (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  pack_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  member text NOT NULL,
  agent_id uuid NOT NULL,
  PRIMARY KEY (pack_id, user_id, member),
  FOREIGN KEY (workspace_id, pack_id) REFERENCES field_pack (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, agent_id) REFERENCES agent (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE "user" DROP COLUMN pack_name;

ALTER TABLE autoresearch
  ADD CONSTRAINT autoresearch_workspace_id_key UNIQUE (workspace_id, id),
  DROP COLUMN budget_usd,
  DROP CONSTRAINT autoresearch_execution_mode_check,
  ADD CONSTRAINT autoresearch_execution_mode_check CHECK (execution_mode IN ('create_card', 'run_only')),
  ADD COLUMN assignee_type text CHECK (assignee_type IN ('agent', 'team')),
  ADD COLUMN assignee_id uuid,
  ADD COLUMN scope_from text NOT NULL DEFAULT 'grounding'
    CHECK (scope_from IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  ADD COLUMN scope_to text NOT NULL DEFAULT 'reporting'
    CHECK (scope_to IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  ADD CONSTRAINT autoresearch_scope_order CHECK (
    array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_from)
    <= array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_to)),
  ADD CONSTRAINT autoresearch_assignee_tuple CHECK (
    (assignee_type IS NULL AND assignee_id IS NULL) OR
    (assignee_type IS NOT NULL AND assignee_type = 'agent' AND assignee_id IS NOT NULL) OR
    (assignee_type IS NOT NULL AND assignee_type = 'team'));

CREATE TABLE autoresearch_team (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  autoresearch_id uuid NOT NULL,
  board_column text NOT NULL CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  pack_id uuid NOT NULL,
  PRIMARY KEY (autoresearch_id, board_column),
  FOREIGN KEY (workspace_id, autoresearch_id) REFERENCES autoresearch (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, pack_id) REFERENCES field_pack (workspace_id, id)
);

ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check;
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'released', 'autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done'));

ALTER TABLE agent_run ADD COLUMN requested_model text NOT NULL DEFAULT '';

-- A workspace's choices for one catalogue connector. No row means the
-- default of the connector's tier.
CREATE TABLE workspace_connector (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
  enabled boolean NOT NULL DEFAULT true,
  -- nonce, sealed key and tag: never the key itself
  key_ciphertext bytea CHECK (octet_length(key_ciphertext) BETWEEN 29 AND 2048),
  licence_confirmed_by uuid REFERENCES "user",
  licence_confirmed_at timestamptz,
  updated_by uuid NOT NULL REFERENCES "user",
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, name),
  CHECK ((licence_confirmed_by IS NULL) = (licence_confirmed_at IS NULL))
);

-- The latest response to one exact connector request in a workspace. A fetch
-- of the same request within a day of fetched_at is answered from its blob.
CREATE TABLE connector_fetch (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
  connector text NOT NULL,
  blob_key text NOT NULL,
  content_type text NOT NULL DEFAULT '',
  truncated boolean NOT NULL,
  fetched_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, request_sha256)
);

ALTER TABLE card_team ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON card_team USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE team_agent ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON team_agent USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE autoresearch_team ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON autoresearch_team USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE workspace_connector ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace_connector USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE connector_fetch ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON connector_fetch USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON card_team, team_agent, autoresearch_team, workspace_connector, connector_fetch TO lykeion_app;

-- Studies the engine should look at: those working a stage column with no run
-- in flight, or with a settled run to act on this round. A done, blocked or
-- Backlog study runs nothing, so it is never looked at.
CREATE OR REPLACE FUNCTION board_candidates() RETURNS TABLE (workspace_id uuid, card_id uuid, board_column text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT c.workspace_id, c.id, c.board_column FROM card c
  WHERE c.ending = '' AND c.flag = '' AND c.state = 'working'
    AND c.board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')
    AND (NOT EXISTS (
          SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
            AND t.board_column = c.board_column AND t.round = c.round
            AND t.status IN ('queued', 'dispatched', 'running'))
        OR EXISTS (
          SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
            AND t.board_column = c.board_column AND t.round = c.round
            AND t.status IN ('completed', 'failed', 'cancelled')))
$$;

-- Autoresearch runs whose status no longer matches their study or agent run:
-- a done study completes its run, a blocked one blocks it for its cause.
CREATE OR REPLACE FUNCTION autoresearch_drift() RETURNS TABLE (workspace_id uuid, run_id uuid, status text, reason text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT r.workspace_id, r.id, f.status, f.reason
  FROM autoresearch_run r
  LEFT JOIN card c ON c.id = r.card_id
  LEFT JOIN agent_run t ON t.id = r.agent_run_id
  CROSS JOIN LATERAL (SELECT
    CASE
      WHEN c.id IS NOT NULL THEN CASE
        WHEN c.ending <> '' THEN 'failed' WHEN c.state = 'done' THEN 'completed'
        WHEN c.state = 'blocked' OR c.flag <> '' THEN 'blocked' ELSE 'running' END
      ELSE CASE t.status
        WHEN 'completed' THEN 'completed' WHEN 'failed' THEN 'failed' WHEN 'cancelled' THEN 'failed' ELSE 'running' END
    END AS status,
    CASE
      WHEN c.id IS NOT NULL THEN CASE
        WHEN c.ending <> '' THEN 'card_' || c.ending WHEN c.state = 'done' THEN ''
        WHEN c.state = 'blocked' THEN c.block_kind WHEN c.flag <> '' THEN c.flag ELSE '' END
      ELSE CASE t.status
        WHEN 'failed' THEN coalesce(t.failure_reason, 'agent_error') WHEN 'cancelled' THEN 'cancelled' ELSE '' END
    END AS reason) f
  WHERE r.status IN ('running', 'blocked') AND (c.id IS NOT NULL OR t.id IS NOT NULL)
    AND (f.status <> r.status OR f.reason <> r.reason)
$$;
