-- Reporting is no longer a stage. Studies and Autoresearch that reach it or are
-- worked by a Reporting Team go, with the Reporting Teams themselves. Follows
-- from and study variables go too.
DO $$
BEGIN
CREATE TEMP TABLE gone_pack ON COMMIT DROP AS
SELECT workspace_id, id FROM field_pack WHERE stage = 'reporting';

CREATE TEMP TABLE gone_card ON COMMIT DROP AS
SELECT id FROM card c
WHERE c.board_column = 'reporting' OR c.scope_to = 'reporting'
  OR c.pack_id IN (SELECT id FROM gone_pack)
  OR EXISTS (SELECT 1 FROM card_team t WHERE t.card_id = c.id AND t.pack_id IN (SELECT id FROM gone_pack));

DELETE FROM team_rating
WHERE card_id IN (SELECT id FROM gone_card) OR pack_id IN (SELECT id FROM gone_pack) OR board_column = 'reporting';
-- card_team, record, agent_run and attachment rows cascade; autoresearch_run,
-- event and webhook_delivery keep theirs with card_id cleared.
DELETE FROM card WHERE id IN (SELECT id FROM gone_card);
DELETE FROM card_team WHERE board_column = 'reporting';
DELETE FROM autoresearch
WHERE scope_to = 'reporting'
  OR id IN (SELECT autoresearch_id FROM autoresearch_team WHERE pack_id IN (SELECT id FROM gone_pack));
DELETE FROM autoresearch_team WHERE board_column = 'reporting';
-- team_agent and field_pack_approval cascade.
DELETE FROM field_pack WHERE id IN (SELECT id FROM gone_pack);
DELETE FROM stage_addon WHERE board_column = 'reporting';
END $$;

ALTER TABLE card
  DROP CONSTRAINT card_board_column_check,
  ADD CONSTRAINT card_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'done')),
  DROP CONSTRAINT card_scope_from_check,
  ADD CONSTRAINT card_scope_from_check
    CHECK (scope_from IN ('grounding', 'hypotheses', 'experimentation', 'validation')),
  DROP CONSTRAINT card_scope_to_check,
  ADD CONSTRAINT card_scope_to_check
    CHECK (scope_to IN ('grounding', 'hypotheses', 'experimentation', 'validation')),
  DROP CONSTRAINT card_scope_order,
  ADD CONSTRAINT card_scope_order CHECK (
    array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation'], scope_from)
    <= array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation'], scope_to)),
  ALTER COLUMN scope_to SET DEFAULT 'validation';

ALTER TABLE autoresearch
  DROP CONSTRAINT autoresearch_scope_from_check,
  ADD CONSTRAINT autoresearch_scope_from_check
    CHECK (scope_from IN ('grounding', 'hypotheses', 'experimentation', 'validation')),
  DROP CONSTRAINT autoresearch_scope_to_check,
  ADD CONSTRAINT autoresearch_scope_to_check
    CHECK (scope_to IN ('grounding', 'hypotheses', 'experimentation', 'validation')),
  DROP CONSTRAINT autoresearch_scope_order,
  ADD CONSTRAINT autoresearch_scope_order CHECK (
    array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation'], scope_from)
    <= array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation'], scope_to)),
  ALTER COLUMN scope_to SET DEFAULT 'validation';

ALTER TABLE card_team
  DROP CONSTRAINT card_team_board_column_check,
  ADD CONSTRAINT card_team_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation'));
ALTER TABLE autoresearch_team
  DROP CONSTRAINT autoresearch_team_board_column_check,
  ADD CONSTRAINT autoresearch_team_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation'));
ALTER TABLE team_rating
  DROP CONSTRAINT team_rating_board_column_check,
  ADD CONSTRAINT team_rating_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation'));
ALTER TABLE stage_addon
  DROP CONSTRAINT stage_addon_board_column_check,
  ADD CONSTRAINT stage_addon_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation'));
ALTER TABLE field_pack
  DROP CONSTRAINT field_pack_stage_check,
  ADD CONSTRAINT field_pack_stage_check
    CHECK (stage IN ('', 'grounding', 'hypotheses', 'experimentation', 'validation'));

CREATE OR REPLACE FUNCTION board_candidates() RETURNS TABLE (workspace_id uuid, card_id uuid, board_column text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT c.workspace_id, c.id, c.board_column FROM card c
  WHERE c.ending = '' AND c.flag = '' AND c.state = 'working'
    AND c.board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation')
    AND (NOT EXISTS (
          SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
            AND t.board_column = c.board_column AND t.round = c.round
            AND t.status IN ('queued', 'dispatched', 'running'))
        OR EXISTS (
          SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
            AND t.board_column = c.board_column AND t.round = c.round
            AND t.status IN ('completed', 'failed', 'cancelled')))
$$;

ALTER TABLE card DROP CONSTRAINT card_follows_end_to_end, DROP COLUMN follows_from, DROP COLUMN env;
ALTER TABLE attachment
  DROP CONSTRAINT attachment_file_metadata_check,
  DROP COLUMN service_env,
  ADD CONSTRAINT attachment_file_metadata_check
    CHECK (kind <> 'file' OR (service_name IS NULL AND service_start IS NULL AND service_client IS NULL));

-- Whether an agent belongs to a Team is asked for every listed agent.
CREATE INDEX team_agent_agent ON team_agent (workspace_id, agent_id);
