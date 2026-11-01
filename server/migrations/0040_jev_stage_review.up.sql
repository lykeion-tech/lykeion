ALTER TABLE card
  ADD COLUMN grounding_criteria jsonb NOT NULL DEFAULT '[]'
    CHECK (jsonb_typeof(grounding_criteria) = 'array' AND octet_length(grounding_criteria::text) <= 4096),
  ADD COLUMN legacy_column text NOT NULL DEFAULT '';

-- A study in progress finishes its current column under the rules it started with.
UPDATE card SET legacy_column = board_column WHERE state IN ('working', 'blocked') AND ending = '';

DROP TRIGGER card_predicates_fixed ON card;
DROP FUNCTION card_predicates_fixed();
ALTER TABLE card DROP COLUMN predicates;

UPDATE card SET block_kind = 'low_score' WHERE block_kind = 'unconfirmed_results';
ALTER TABLE card DROP CONSTRAINT card_block_kind_check,
  ADD CONSTRAINT card_block_kind_check CHECK (block_kind IN ('', 'low_score'));

DELETE FROM inbox_item WHERE kind = 'preregistered';
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check;
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'released', 'autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done'));

DELETE FROM record WHERE kind = 'preregistration';
