CREATE TABLE lesson_job (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid,
  decision_id uuid,
  lesson_id uuid,
  triggers text[] NOT NULL DEFAULT '{}',
  due_at timestamptz NOT NULL,
  batch_id text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'submitted', 'done', 'failed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, decision_id) REFERENCES inbox_item (workspace_id, id) ON DELETE SET NULL (decision_id),
  FOREIGN KEY (workspace_id, lesson_id) REFERENCES lesson (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX lesson_job_due ON lesson_job (due_at, id) WHERE status = 'queued';
ALTER TABLE lesson_job ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lesson_job USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON lesson_job TO lykeion_app;
CREATE FUNCTION due_lesson_jobs(lim int) RETURNS SETOF lesson_job
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT * FROM public.lesson_job WHERE status = 'queued' AND due_at <= now()
  ORDER BY due_at, id LIMIT GREATEST(0, $1)
$$;
REVOKE ALL ON FUNCTION due_lesson_jobs(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION due_lesson_jobs(int) TO lykeion_app;
CREATE INDEX lesson_card_queued ON lesson_job(workspace_id,card_id,created_at,id)
 WHERE status='queued' AND batch_id='' AND lesson_id IS NULL;
ALTER TABLE lesson_job ADD UNIQUE(workspace_id,id), ADD COLUMN failures integer NOT NULL DEFAULT 0;
CREATE TABLE lesson_job_input (
 workspace_id uuid NOT NULL, job_id uuid PRIMARY KEY, versions jsonb NOT NULL, rewrite_version text NOT NULL,
 FOREIGN KEY(workspace_id,job_id) REFERENCES lesson_job(workspace_id,id) ON DELETE CASCADE
);
ALTER TABLE lesson_job_input ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lesson_job_input USING(workspace_id=app_workspace()) WITH CHECK(workspace_id=app_workspace());
GRANT SELECT,INSERT,UPDATE,DELETE ON lesson_job_input TO lykeion_app;
CREATE INDEX lesson_job_poll ON lesson_job(due_at,batch_id) WHERE status='submitted';
CREATE FUNCTION pending_lesson_batch() RETURNS TABLE(id uuid,workspace_id uuid,batch_id text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT j.id,j.workspace_id,j.batch_id FROM public.lesson_job j WHERE j.status='submitted' AND j.batch_id=(
 SELECT q.batch_id FROM public.lesson_job q WHERE q.status='submitted' AND q.due_at<=now()
 ORDER BY q.due_at,q.id LIMIT 1) ORDER BY j.id LIMIT 50
$$;
REVOKE ALL ON FUNCTION pending_lesson_batch() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pending_lesson_batch() TO lykeion_app;
