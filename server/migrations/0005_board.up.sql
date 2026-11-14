-- round counts every new Working phase of a card, across columns; agent runs and
-- records carry it, so a later round never mixes with an earlier one.
ALTER TABLE card ADD COLUMN round int NOT NULL DEFAULT 0;

-- Why the next round redoes work: the gate's route reason or a person's
-- send-back reason. The card holds it until the round is queued; each redone
-- role's agent run carries its own copy into its prompt.
ALTER TABLE card ADD COLUMN handoff_note text NOT NULL DEFAULT '';
ALTER TABLE agent_run ADD COLUMN handoff_note text NOT NULL DEFAULT '';

-- While one engine pass judges a card outside a transaction (Jev, blob reads),
-- passes on every server node leave the card alone until this time. A node
-- that dies mid-call leaves a lease that simply expires.
ALTER TABLE card ADD COLUMN gating_until timestamptz;

-- Text compared for duplicates: case and runs of whitespace ignored.
CREATE FUNCTION norm_text(text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
  SELECT btrim(lower(regexp_replace($1, '\s+', ' ', 'g')))
$$;

-- Queued agent runs whose responsible user has no online runtime for their harness
-- wait on runtime_offline; the reason clears once one is back. Rows carrying
-- another reason are left alone, and only rows that change are written.
CREATE FUNCTION mark_queue_offline() RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  WITH want AS (
    SELECT t.id, CASE WHEN EXISTS (
        SELECT 1 FROM runtime r WHERE r.workspace_id = t.workspace_id AND r.owner_id = t.responsible_user_id
          AND r.harness = t.harness AND r.status = 'online')
      THEN NULL ELSE 'runtime_offline' END AS reason
    FROM agent_run t
    WHERE t.status = 'queued' AND (t.queue_reason IS NULL OR t.queue_reason = 'runtime_offline'))
  UPDATE agent_run SET queue_reason = want.reason FROM want
  WHERE agent_run.id = want.id AND agent_run.queue_reason IS DISTINCT FROM want.reason
$$;
REVOKE ALL ON FUNCTION mark_queue_offline() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION mark_queue_offline() TO lykeion_app;

-- Cards the engine should look at: Backlog cards (for WIP pull) and stage
-- cards with no run in flight or a settled outcome to inspect this round.
CREATE FUNCTION board_candidates() RETURNS TABLE (workspace_id uuid, card_id uuid, board_column text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT c.workspace_id, c.id, c.board_column FROM card c
  WHERE c.ending = '' AND c.flag = '' AND (
    c.board_column = 'backlog'
    OR (c.board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')
        AND (NOT EXISTS (
          SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
            AND t.board_column = c.board_column AND t.round = c.round
            AND t.status IN ('queued', 'dispatched', 'running'))
          OR EXISTS (
            SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
              AND t.board_column = c.board_column AND t.round = c.round
              AND t.status IN ('completed', 'failed', 'cancelled')))))
$$;
REVOKE ALL ON FUNCTION board_candidates() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION board_candidates() TO lykeion_app;
