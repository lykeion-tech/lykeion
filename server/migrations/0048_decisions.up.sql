DELETE FROM inbox_item WHERE kind IN ('escalated', 'released');
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check,
  ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done', 'waiting_for_machine', 'needs_decision', 'open_doubts',
   'agent_decision', 'machine_offline'));

ALTER TABLE inbox_item
  ADD COLUMN options jsonb NOT NULL DEFAULT '[]',
  ADD COLUMN default_option text NOT NULL DEFAULT '',
  ADD COLUMN status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'decided', 'expired', 'dismissed', 'cancelled')),
  ADD COLUMN expires_at timestamptz,
  ADD COLUMN reminded_at timestamptz,
  ADD COLUMN chosen_option text NOT NULL DEFAULT '',
  ADD COLUMN guidance text NOT NULL DEFAULT '',
  ADD COLUMN decided_by uuid REFERENCES "user"(id),
  ADD COLUMN decided_at timestamptz,
  ADD COLUMN raised_by text NOT NULL DEFAULT 'system' CHECK (raised_by IN ('system', 'agent')),
  ADD COLUMN raised_run_id uuid REFERENCES agent_run(id),
  ADD COLUMN from_member text NOT NULL DEFAULT '',
  ADD COLUMN decision_kind text NOT NULL DEFAULT '' CHECK (decision_kind IN ('', 'scope', 'change_question', 'ethics', 'cost', 'access')),
  ADD COLUMN decide_by text NOT NULL DEFAULT '',
  ADD COLUMN decide_by_date date,
  ADD COLUMN snoozed_until timestamptz,
  ADD COLUMN kept boolean NOT NULL DEFAULT false,
  ADD COLUMN archived_at timestamptz,
  ADD COLUMN touched_at timestamptz NOT NULL DEFAULT now();

CREATE UNIQUE INDEX inbox_item_one_agent_decision ON inbox_item (card_id)
  WHERE kind = 'agent_decision' AND status = 'open';
CREATE INDEX inbox_item_due ON inbox_item (expires_at)
  WHERE status = 'open' AND expires_at IS NOT NULL;

ALTER TABLE card DROP CONSTRAINT card_block_kind_check,
  ADD CONSTRAINT card_block_kind_check CHECK (block_kind IN ('', 'low_score', 'decision', 'parked'));

-- Resolved items are history; open stops become decisions that park the study after a week.
UPDATE inbox_item SET status = 'decided', decided_at = resolved_at WHERE resolved_at IS NOT NULL;

UPDATE inbox_item SET default_option = 'park', expires_at = now() + interval '7 days',
  options = '[{"id":"redo","label":"Redo with guidance","consequence":"The column runs again with your guidance as its brief."},
    {"id":"close","label":"Close as a negative result","consequence":"The study ends here and is recorded as a negative result."},
    {"id":"park","label":"Park","consequence":"The study stops here; anyone who can open it may redo it later."}]'
WHERE resolved_at IS NULL AND (kind = 'blocked' OR (kind = 'needs_decision' AND cause IS DISTINCT FROM 'machine_failures'));

UPDATE inbox_item SET default_option = 'park', expires_at = now() + interval '7 days',
  options = '[{"id":"try_again","label":"Try again","consequence":"The parked runs are queued again on their machine."},
    {"id":"park","label":"Park","consequence":"The study stops here; anyone who can open it may redo it later."}]'
WHERE resolved_at IS NULL AND kind = 'needs_decision' AND cause = 'machine_failures';
