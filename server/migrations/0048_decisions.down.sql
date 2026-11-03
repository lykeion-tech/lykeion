DROP INDEX inbox_item_due;
DROP INDEX inbox_item_one_agent_decision;
DELETE FROM inbox_item WHERE kind IN ('agent_decision', 'machine_offline');
ALTER TABLE inbox_item
  DROP COLUMN options, DROP COLUMN default_option, DROP COLUMN status, DROP COLUMN expires_at,
  DROP COLUMN reminded_at, DROP COLUMN chosen_option, DROP COLUMN guidance, DROP COLUMN decided_by,
  DROP COLUMN decided_at, DROP COLUMN raised_by, DROP COLUMN raised_run_id, DROP COLUMN from_member,
  DROP COLUMN decision_kind, DROP COLUMN decide_by, DROP COLUMN decide_by_date, DROP COLUMN snoozed_until,
  DROP COLUMN kept, DROP COLUMN archived_at, DROP COLUMN touched_at;
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check,
  ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'released', 'autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done', 'waiting_for_machine', 'needs_decision', 'open_doubts'));
UPDATE card SET block_kind = 'low_score' WHERE block_kind IN ('decision', 'parked');
ALTER TABLE card DROP CONSTRAINT card_block_kind_check,
  ADD CONSTRAINT card_block_kind_check CHECK (block_kind IN ('', 'low_score'));
