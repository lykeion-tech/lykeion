ALTER TABLE agent_run
  ADD COLUMN pinned_runtime_id uuid,
  ADD CONSTRAINT agent_run_pinned_runtime_fkey FOREIGN KEY (pinned_runtime_id, workspace_id) REFERENCES runtime (id, workspace_id) ON DELETE SET NULL (pinned_runtime_id),
  DROP CONSTRAINT agent_run_kind_check,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch', 'rerun', 'aristotle', 'report')),
  DROP CONSTRAINT agent_run_kind_shape,
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL AND lab_chat_id IS NULL)
    OR (kind = 'rerun' AND card_id IS NOT NULL AND lab_chat_id IS NULL)
    OR (kind = 'aristotle' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NOT NULL)
    OR (kind = 'report' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NULL));

CREATE TABLE report_build (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  agent_run_id uuid,
  status text NOT NULL CHECK (status IN ('queued', 'judging', 'done', 'failed', 'skipped')),
  checks_status text NOT NULL CHECK (checks_status IN ('pending', 'done', 'unavailable')),
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  PRIMARY KEY (workspace_id, card_id),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE SET NULL (agent_run_id)
);

CREATE TABLE report_concept (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  key text NOT NULL,
  label text NOT NULL,
  type text NOT NULL,
  PRIMARY KEY (workspace_id, card_id, key),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE
);

-- subject and object are concept keys, except that a mentioned_in object is a node id.
CREATE TABLE report_fact (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  subject text NOT NULL,
  relation text NOT NULL CHECK (relation IN ('is_a', 'part_of', 'associated_with', 'increases', 'decreases', 'measured_by', 'mentioned_in')),
  object text NOT NULL,
  quote text NOT NULL,
  source_node text NOT NULL,
  p double precision NOT NULL,
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX report_fact_card ON report_fact (workspace_id, card_id);

CREATE TABLE report_link (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  start_byte int NOT NULL,
  end_byte int NOT NULL,
  node_id text NOT NULL,
  p double precision NOT NULL,
  PRIMARY KEY (workspace_id, card_id, start_byte),
  CHECK (end_byte > start_byte),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE
);

-- check_id is figure:<output node>, table:<output node> or novelty:<paper node>;
-- a novelty check's choice is confirms, contradicts or unrelated.
CREATE TABLE report_check (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  node_id text NOT NULL,
  check_id text NOT NULL,
  question text NOT NULL,
  choice text NOT NULL,
  p double precision NOT NULL,
  status text NOT NULL CHECK (status IN ('pass', 'unclear', 'fail')),
  evidence text NOT NULL,
  PRIMARY KEY (workspace_id, card_id, node_id, check_id),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE report_build ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON report_build USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE report_concept ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON report_concept USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE report_fact ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON report_fact USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE report_link ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON report_link USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE report_check ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON report_check USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON report_build, report_concept, report_fact, report_link, report_check TO lykeion_app;

-- A report run waits only for the runtime it is pinned to; a rerun for its
-- study's machine; every other run for an online runtime of its owner's with its harness.
CREATE OR REPLACE FUNCTION mark_queue_offline() RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  WITH want AS (
    SELECT t.id, CASE WHEN EXISTS (
        SELECT 1 FROM runtime r WHERE r.workspace_id = t.workspace_id AND r.status = 'online'
          AND ((t.kind NOT IN ('rerun', 'report') AND r.owner_id = t.responsible_user_id AND r.harness = t.harness)
            OR (t.kind = 'report' AND r.id = t.pinned_runtime_id)
            OR (t.kind = 'rerun' AND ((c.machine IS NULL AND r.owner_id = t.responsible_user_id) OR r.daemon_id = c.machine))))
      THEN NULL ELSE 'runtime_offline' END AS reason
    FROM agent_run t LEFT JOIN card c ON c.id = t.card_id
    WHERE t.status = 'queued' AND (t.queue_reason IS NULL OR t.queue_reason = 'runtime_offline'))
  UPDATE agent_run SET queue_reason = want.reason FROM want
  WHERE agent_run.id = want.id AND agent_run.queue_reason IS DISTINCT FROM want.reason
$$;

-- Builds a restart left unfinished, across workspaces: judging with a run that
-- settled two minutes ago (or whose run row is gone), or checks still pending
-- two minutes after the build began.
CREATE FUNCTION report_builds_to_resume() RETURNS TABLE (workspace_id uuid, card_id uuid, judging boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT b.workspace_id, b.card_id, b.status = 'judging'
  FROM report_build b
  LEFT JOIN agent_run r ON r.id = b.agent_run_id AND r.workspace_id = b.workspace_id
  WHERE (b.status = 'judging' AND ((b.agent_run_id IS NULL AND b.created_at < now() - interval '2 minutes')
          OR (r.status IN ('completed', 'failed', 'cancelled') AND r.completed_at < now() - interval '2 minutes')))
    OR (b.checks_status = 'pending' AND b.created_at < now() - interval '2 minutes')
  ORDER BY b.created_at
$$;
REVOKE ALL ON FUNCTION report_builds_to_resume() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION report_builds_to_resume() TO lykeion_app;
