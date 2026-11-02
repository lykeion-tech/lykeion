ALTER TABLE agent_run
  DROP CONSTRAINT agent_run_kind_check,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch', 'rerun')),
  DROP CONSTRAINT agent_run_kind_shape,
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL)
    OR (kind = 'rerun' AND card_id IS NOT NULL)),
  -- A rerun replays recorded scripts without an agent CLI, so it names none.
  DROP CONSTRAINT agent_run_harness_check,
  ADD CONSTRAINT agent_run_harness_check CHECK (harness IN ('claude', 'codex', 'cursor', 'antigravity', '')),
  ADD CONSTRAINT agent_run_rerun_harness CHECK ((kind = 'rerun') = (harness = ''));

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

-- A rerun runs no agent CLI: it waits only while no runtime of its study's
-- machine, or of its owner's machines when the study chose none, is online.
CREATE OR REPLACE FUNCTION mark_queue_offline() RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  WITH want AS (
    SELECT t.id, CASE WHEN EXISTS (
        SELECT 1 FROM runtime r WHERE r.workspace_id = t.workspace_id AND r.status = 'online'
          AND ((t.kind <> 'rerun' AND r.owner_id = t.responsible_user_id AND r.harness = t.harness)
            OR (t.kind = 'rerun' AND ((c.machine IS NULL AND r.owner_id = t.responsible_user_id) OR r.daemon_id = c.machine))))
      THEN NULL ELSE 'runtime_offline' END AS reason
    FROM agent_run t LEFT JOIN card c ON c.id = t.card_id
    WHERE t.status = 'queued' AND (t.queue_reason IS NULL OR t.queue_reason = 'runtime_offline'))
  UPDATE agent_run SET queue_reason = want.reason FROM want
  WHERE agent_run.id = want.id AND agent_run.queue_reason IS DISTINCT FROM want.reason
$$;
