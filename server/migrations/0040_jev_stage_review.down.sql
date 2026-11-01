ALTER TABLE card ADD COLUMN predicates jsonb NOT NULL DEFAULT '[]'
  CHECK (jsonb_typeof(predicates) = 'array' AND octet_length(predicates::text) <= 8192);

CREATE FUNCTION card_predicates_fixed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.predicates IS DISTINCT FROM OLD.predicates THEN
    RAISE EXCEPTION 'a study''s pre-registered results cannot change' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER card_predicates_fixed BEFORE UPDATE ON card
  FOR EACH ROW EXECUTE FUNCTION card_predicates_fixed();

ALTER TABLE card DROP CONSTRAINT card_block_kind_check,
  ADD CONSTRAINT card_block_kind_check CHECK (block_kind IN ('', 'low_score', 'unconfirmed_results'));

ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check;
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'released', 'autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done', 'preregistered'));

ALTER TABLE card DROP COLUMN grounding_criteria, DROP COLUMN legacy_column;
