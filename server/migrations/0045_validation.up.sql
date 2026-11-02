ALTER TABLE agent_run
  DROP CONSTRAINT agent_run_kind_check,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch', 'rerun')),
  DROP CONSTRAINT agent_run_kind_shape,
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL)
    OR (kind = 'rerun' AND card_id IS NOT NULL));

-- A Validation column worked under the old protocol starts again as a fresh round under the new review rules.
-- Blocked cards stay blocked; their owner's comment restarts them as before.
WITH restarted AS (
  SELECT id, workspace_id FROM card
  WHERE board_column = 'validation' AND state IN ('working', 'gating') AND ending = ''
), cancelled AS (
  UPDATE agent_run r SET status = 'cancelled', completed_at = now()
  FROM restarted c
  WHERE r.workspace_id = c.workspace_id AND r.card_id = c.id AND r.kind = 'role'
    AND r.status IN ('queued', 'dispatched', 'running')
)
UPDATE card SET round = round + 1, redos = 0, legacy_column = '',
  handoff_note = 'Validation restarted under the new review rules.', version = version + 1
FROM restarted WHERE card.id = restarted.id;
