DROP FUNCTION report_builds_to_resume();
DROP TABLE report_check, report_link, report_fact, report_concept, report_build;
DELETE FROM agent_run WHERE kind = 'report';
ALTER TABLE agent_run
  DROP CONSTRAINT agent_run_kind_shape,
  DROP CONSTRAINT agent_run_kind_check,
  DROP COLUMN pinned_runtime_id,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch', 'rerun', 'aristotle')),
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL AND lab_chat_id IS NULL)
    OR (kind = 'rerun' AND card_id IS NOT NULL AND lab_chat_id IS NULL)
    OR (kind = 'aristotle' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NOT NULL));

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
