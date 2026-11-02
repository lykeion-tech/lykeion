DELETE FROM agent_run WHERE kind = 'rerun';
ALTER TABLE agent_run
  DROP CONSTRAINT agent_run_kind_shape,
  DROP CONSTRAINT agent_run_kind_check,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch')),
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL));
