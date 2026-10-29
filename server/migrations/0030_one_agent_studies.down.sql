-- Deleted studies, Autoresearch and Teams are not restored.
DROP INDEX team_agent_agent;

ALTER TABLE attachment
  DROP CONSTRAINT attachment_file_metadata_check,
  ADD COLUMN service_env text[] NOT NULL DEFAULT '{}',
  ADD CONSTRAINT attachment_file_metadata_check CHECK (kind <> 'file' OR (service_name IS NULL AND service_start IS NULL AND service_client IS NULL AND cardinality(service_env) = 0));
ALTER TABLE card
  ADD COLUMN follows_from uuid[] NOT NULL DEFAULT '{}',
  ADD COLUMN env jsonb NOT NULL DEFAULT '{}'
    CHECK (jsonb_typeof(env) = 'object' AND NOT jsonb_path_exists(env, 'strict $.* ? (@.type() != "string")'));

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

ALTER TABLE field_pack
  DROP CONSTRAINT field_pack_stage_check,
  ADD CONSTRAINT field_pack_stage_check
    CHECK (stage IN ('', 'grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'));
ALTER TABLE stage_addon
  DROP CONSTRAINT stage_addon_board_column_check,
  ADD CONSTRAINT stage_addon_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'));
ALTER TABLE team_rating
  DROP CONSTRAINT team_rating_board_column_check,
  ADD CONSTRAINT team_rating_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'));
ALTER TABLE autoresearch_team
  DROP CONSTRAINT autoresearch_team_board_column_check,
  ADD CONSTRAINT autoresearch_team_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'));
ALTER TABLE card_team
  DROP CONSTRAINT card_team_board_column_check,
  ADD CONSTRAINT card_team_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'));

ALTER TABLE autoresearch
  DROP CONSTRAINT autoresearch_scope_from_check,
  ADD CONSTRAINT autoresearch_scope_from_check
    CHECK (scope_from IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  DROP CONSTRAINT autoresearch_scope_to_check,
  ADD CONSTRAINT autoresearch_scope_to_check
    CHECK (scope_to IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  DROP CONSTRAINT autoresearch_scope_order,
  ADD CONSTRAINT autoresearch_scope_order CHECK (
    array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_from)
    <= array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_to)),
  ALTER COLUMN scope_to SET DEFAULT 'reporting';

ALTER TABLE card
  DROP CONSTRAINT card_board_column_check,
  ADD CONSTRAINT card_board_column_check
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting', 'done')),
  DROP CONSTRAINT card_scope_from_check,
  ADD CONSTRAINT card_scope_from_check
    CHECK (scope_from IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  DROP CONSTRAINT card_scope_to_check,
  ADD CONSTRAINT card_scope_to_check
    CHECK (scope_to IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  DROP CONSTRAINT card_scope_order,
  ADD CONSTRAINT card_scope_order CHECK (
    array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_from)
    <= array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'], scope_to)),
  ALTER COLUMN scope_to SET DEFAULT 'reporting',
  ADD CONSTRAINT card_follows_end_to_end CHECK (
    cardinality(follows_from) = 0 OR (scope_from = 'grounding' AND scope_to = 'reporting'));
