ALTER TABLE agent_run DROP CONSTRAINT agent_run_failure_reason_check,
  ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline','timeout','provider_network','context_exhausted','agent_error','runtime_access_denied','coresearch_access_revoked',
    'sign_in','cli_missing','cli_too_old','door','cli_broken','sandbox','fence_breach','hand_in_invalid','lab_context_changed'));

CREATE TABLE lab_chat (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  owner_id uuid NOT NULL REFERENCES "user",
  title text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, id, owner_id)
);

ALTER TABLE agent_run
  ADD COLUMN lab_chat_id uuid,
  ADD CONSTRAINT agent_run_lab_chat_fk FOREIGN KEY (workspace_id, lab_chat_id) REFERENCES lab_chat (workspace_id, id) ON DELETE CASCADE,
  DROP CONSTRAINT agent_run_kind_check,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch', 'rerun', 'aristotle')),
  DROP CONSTRAINT agent_run_kind_shape,
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL AND lab_chat_id IS NULL)
    OR (kind = 'rerun' AND card_id IS NOT NULL AND lab_chat_id IS NULL)
    OR (kind = 'aristotle' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL AND lab_chat_id IS NOT NULL));

CREATE TABLE lab_message (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  chat_id uuid NOT NULL,
  owner_id uuid NOT NULL REFERENCES "user",
  kind text NOT NULL CHECK (kind IN ('question', 'answer', 'failure')),
  body text NOT NULL,
  statements jsonb NOT NULL DEFAULT '[]',
  context_card_id uuid,
  context_ref text,
  agent_run_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, chat_id, owner_id) REFERENCES lab_chat (workspace_id, id, owner_id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, context_card_id) REFERENCES card (workspace_id, id) ON DELETE SET NULL (context_card_id),
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE SET NULL (agent_run_id)
);
CREATE INDEX lab_message_chat ON lab_message (workspace_id, chat_id, created_at, id);

CREATE TABLE lab_read (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_run_id uuid NOT NULL,
  ref text NOT NULL,
  via text NOT NULL DEFAULT '',
  read_version text NOT NULL DEFAULT '',
  attempt integer NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);

CREATE INDEX lab_read_run_ref ON lab_read (agent_run_id, ref);

-- Keyset navigation reads one bounded slice of a study under the run lock.
CREATE INDEX lab_study_record_links ON record (workspace_id,card_id,id);
CREATE INDEX lab_study_comment_links ON comment (workspace_id,card_id,id);
CREATE INDEX lab_study_decision_links ON inbox_item (workspace_id,card_id,id)
  WHERE jsonb_array_length(options)>0 OR question_set IS NOT NULL;

CREATE TABLE lesson (
  directory_key uuid NOT NULL DEFAULT gen_random_uuid(),
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('lab', 'preference')),
  text text NOT NULL,
  cites jsonb NOT NULL DEFAULT '[]',
  studies uuid[] NOT NULL DEFAULT '{}',
  generalises boolean NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'set_aside')),
  set_aside_reason text NOT NULL DEFAULT '',
  model text NOT NULL,
  written_at timestamptz NOT NULL DEFAULT now(),
  checked_at timestamptz,
  CHECK (directory_key <> id),
  UNIQUE (workspace_id, id)
);
-- A navigation cursor never has to disclose the reference of a stale lesson.
CREATE UNIQUE INDEX lab_lesson_directory ON lesson (workspace_id,directory_key)
 WHERE status='active' AND checked_at IS NOT NULL;

CREATE TABLE lesson_flag (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  lesson_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES "user",
  reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, lesson_id) REFERENCES lesson (workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE lesson_counter (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  lesson_id uuid NOT NULL,
  ref text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, lesson_id) REFERENCES lesson (workspace_id, id) ON DELETE CASCADE
);
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_workspace_id_id_key UNIQUE (workspace_id, id);
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
CREATE TABLE lesson_card (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  board_column text NOT NULL,
  PRIMARY KEY (workspace_id, card_id),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE
);
CREATE TABLE lab_cursor (
  consumer text PRIMARY KEY,
  last_event_id bigint NOT NULL DEFAULT 0
);
REVOKE ALL ON lab_cursor FROM PUBLIC, lykeion_app;

CREATE FUNCTION lab_run_can_read(chat uuid, owner uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT EXISTS (SELECT 1 FROM public.agent_run r
    WHERE r.workspace_id = app_workspace() AND r.id = app_user()
      AND r.kind = 'aristotle' AND r.status = 'running'
      AND r.lab_chat_id = chat AND r.requester_id = owner)
$$;
REVOKE ALL ON FUNCTION lab_run_can_read(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_run_can_read(uuid, uuid) TO lykeion_app;

ALTER TABLE lab_chat ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner ON lab_chat USING (workspace_id = app_workspace() AND owner_id = app_user()) WITH CHECK (workspace_id = app_workspace() AND owner_id = app_user());
CREATE POLICY run_read ON lab_chat FOR SELECT USING (workspace_id = app_workspace() AND lab_run_can_read(id, owner_id));
ALTER TABLE lab_message ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner ON lab_message USING (workspace_id = app_workspace() AND owner_id = app_user()) WITH CHECK (workspace_id = app_workspace() AND owner_id = app_user());
CREATE POLICY run_read ON lab_message FOR SELECT USING (workspace_id = app_workspace() AND lab_run_can_read(chat_id, owner_id));

ALTER TABLE lab_read ENABLE ROW LEVEL SECURITY;
-- A read trail belongs to its private turn, even when its facts are shared.
CREATE POLICY asker_reads ON lab_read FOR SELECT USING (
 workspace_id=app_workspace() AND EXISTS (
 SELECT 1 FROM agent_run r JOIN member m ON (m.workspace_id,m.user_id)=(r.workspace_id,r.requester_id)
 WHERE r.workspace_id=lab_read.workspace_id AND r.id=lab_read.agent_run_id
 AND r.kind='aristotle' AND r.requester_id=app_user()));
CREATE POLICY asker_logs ON lab_read FOR INSERT WITH CHECK (
 workspace_id=app_workspace() AND EXISTS (
 SELECT 1 FROM agent_run r JOIN member m ON (m.workspace_id,m.user_id)=(r.workspace_id,r.requester_id)
 WHERE r.workspace_id=lab_read.workspace_id AND r.id=lab_read.agent_run_id
 AND r.kind='aristotle' AND r.status='running' AND r.requester_id=app_user()));
ALTER TABLE lesson ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lesson USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE lesson_flag ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lesson_flag USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE lesson_counter ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lesson_counter USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE lesson_job ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lesson_job USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE lesson_card ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lesson_card USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON lab_chat, lab_message, lab_read, lesson, lesson_flag, lesson_counter, lesson_job, lesson_card TO lykeion_app;

CREATE FUNCTION lab_events_after(consumer text, lim int) RETURNS SETOF event
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT e.* FROM public.event e
  WHERE e.id > COALESCE((SELECT c.last_event_id FROM public.lab_cursor c WHERE c.consumer = $1), 0)
  ORDER BY e.id LIMIT GREATEST(0, $2)
$$;
CREATE FUNCTION lab_advance(consumer text, last bigint) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  INSERT INTO public.lab_cursor AS cursor_row (consumer, last_event_id) VALUES ($1, GREATEST(0, $2))
  ON CONFLICT (consumer) DO UPDATE SET last_event_id = GREATEST(cursor_row.last_event_id, excluded.last_event_id)
$$;
CREATE FUNCTION due_lesson_jobs(lim int) RETURNS SETOF lesson_job
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT * FROM public.lesson_job WHERE status = 'queued' AND due_at <= now()
  ORDER BY due_at, id LIMIT GREATEST(0, $1)
$$;
REVOKE ALL ON FUNCTION lab_events_after(text, int), lab_advance(text, bigint), due_lesson_jobs(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_events_after(text, int), lab_advance(text, bigint), due_lesson_jobs(int) TO lykeion_app;

-- Delivery rows are inserted in the producer's transaction. Unlike a high-water
-- event ID, their presence cannot skip a lower ID that commits later. Acknowledged
-- rows are removed; pending rows also disappear with their source event.
CREATE TABLE lab_event_consumer (consumer text PRIMARY KEY);
INSERT INTO lab_event_consumer VALUES ('indexer');
CREATE TABLE lab_event_delivery (
 consumer text NOT NULL REFERENCES lab_event_consumer ON DELETE CASCADE,
 event_id bigint NOT NULL REFERENCES event(id) ON DELETE CASCADE,
 workspace_id uuid NOT NULL,
 PRIMARY KEY (consumer,event_id)
);
-- Owner and SECURITY DEFINER queue operations bypass RLS; app has no direct grants.
ALTER TABLE lab_event_delivery ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lab_event_delivery USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE INDEX lab_event_delivery_workspace ON lab_event_delivery(consumer,workspace_id,event_id);
REVOKE ALL ON lab_event_consumer, lab_event_delivery FROM PUBLIC, lykeion_app;
CREATE FUNCTION lab_enqueue_event() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 INSERT INTO public.lab_event_delivery(consumer,event_id,workspace_id)
 SELECT c.consumer,NEW.id,NEW.workspace_id FROM public.lab_event_consumer c
 WHERE c.consumer <> 'lessons' OR (NEW.card_id IS NOT NULL AND (
   (NEW.type='card.steered' AND (NEW.payload ? 'column' OR NEW.payload ? 'state'))
   OR (NEW.type='inbox.resolved' AND NEW.payload ? 'inbox_item_id')));
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION lab_enqueue_event() FROM PUBLIC;
CREATE TRIGGER lab_enqueue_event AFTER INSERT ON event FOR EACH ROW EXECUTE FUNCTION lab_enqueue_event();
INSERT INTO lab_event_delivery(consumer,event_id,workspace_id) SELECT c.consumer,e.id,e.workspace_id FROM lab_event_consumer c CROSS JOIN event e;

CREATE FUNCTION lab_pending_events(consumer text, lim int) RETURNS TABLE(id bigint, workspace_id uuid, type text, card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT e.id,e.workspace_id,e.type,e.card_id FROM public.lab_event_delivery d JOIN public.event e ON e.id=d.event_id
 WHERE d.consumer=$1 ORDER BY e.id LIMIT LEAST(200,GREATEST(0,$2))
$$;
CREATE FUNCTION lab_ack_event(consumer text, event_id bigint) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=public AS $$
 DELETE FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.event_id=$2
$$;
CREATE FUNCTION lab_index_workspaces_after(after_id uuid, lim int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT w.id FROM public.workspace w WHERE w.id>$1 ORDER BY w.id LIMIT LEAST(20,GREATEST(0,$2))
$$;
CREATE FUNCTION lab_index_workspace_exists(workspace_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT EXISTS(SELECT 1 FROM public.workspace w WHERE w.id=$1)
$$;
REVOKE ALL ON FUNCTION lab_pending_events(text,int), lab_ack_event(text,bigint), lab_index_workspaces_after(uuid,int), lab_index_workspace_exists(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_pending_events(text,int), lab_ack_event(text,bigint), lab_index_workspaces_after(uuid,int), lab_index_workspace_exists(uuid) TO lykeion_app;

CREATE FUNCTION lab_pending_workspace_events(consumer text, after_workspace uuid, lim int)
RETURNS TABLE(id bigint,workspace_id uuid,type text,card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 WITH chosen AS (SELECT COALESCE(
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.workspace_id>$2 ORDER BY d.workspace_id LIMIT 1),
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 ORDER BY d.workspace_id LIMIT 1)) AS ws)
 SELECT e.id,e.workspace_id,e.type,e.card_id FROM public.lab_event_delivery d
 JOIN chosen c ON c.ws=d.workspace_id JOIN public.event e ON e.id=d.event_id
 WHERE d.consumer=$1 ORDER BY d.event_id LIMIT LEAST(200,GREATEST(0,$3))
$$;
REVOKE ALL ON FUNCTION lab_pending_workspace_events(text,uuid,int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_pending_workspace_events(text,uuid,int) TO lykeion_app;

-- Appending dirty refs under an inbox row lock must never acquire the workspace
-- event lock: card deletion takes that event lock before cascading inbox rows.
CREATE TABLE lab_fact_dirty (
 consumer text NOT NULL REFERENCES lab_event_consumer ON DELETE CASCADE,
 id bigserial NOT NULL,
 workspace_id uuid NOT NULL,
 ref text NOT NULL,
 card_id uuid,
 PRIMARY KEY(consumer,id)
);
-- Owner and SECURITY DEFINER queue operations bypass RLS; app has no direct grants.
ALTER TABLE lab_fact_dirty ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON lab_fact_dirty USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE INDEX lab_fact_dirty_workspace ON lab_fact_dirty(consumer,workspace_id,id);
REVOKE ALL ON lab_fact_dirty FROM PUBLIC,lykeion_app;
CREATE FUNCTION lab_decision_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF (NEW.card_id IS NOT NULL OR OLD.card_id IS NOT NULL)
  AND (jsonb_array_length(NEW.options)>0 OR NEW.question_set IS NOT NULL
   OR jsonb_array_length(OLD.options)>0 OR OLD.question_set IS NOT NULL)
  AND NEW IS DISTINCT FROM OLD THEN
  INSERT INTO public.lab_fact_dirty(consumer,workspace_id,ref,card_id)
  SELECT c.consumer,NEW.workspace_id,'decision:'||NEW.id::text,COALESCE(NEW.card_id,OLD.card_id)
  FROM public.lab_event_consumer c;
 END IF;
 RETURN NEW;
END $$;

REVOKE ALL ON FUNCTION lab_decision_changed() FROM PUBLIC;
CREATE TRIGGER lab_decision_changed AFTER UPDATE ON inbox_item FOR EACH ROW EXECUTE FUNCTION lab_decision_changed();

CREATE FUNCTION lab_pending_workspace_work(consumer text,after_workspace uuid,lim int)
RETURNS TABLE(source text,id bigint,workspace_id uuid,type text,card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 WITH candidates AS (
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.workspace_id>$2 ORDER BY d.workspace_id LIMIT 1)
  UNION ALL (SELECT d.workspace_id FROM public.lab_fact_dirty d WHERE d.consumer=$1 AND d.workspace_id>$2 ORDER BY d.workspace_id LIMIT 1)
 ), fallback AS (
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 ORDER BY d.workspace_id LIMIT 1)
  UNION ALL (SELECT d.workspace_id FROM public.lab_fact_dirty d WHERE d.consumer=$1 ORDER BY d.workspace_id LIMIT 1)
 ), chosen AS (SELECT COALESCE((SELECT c.workspace_id FROM candidates c ORDER BY c.workspace_id LIMIT 1),
  (SELECT f.workspace_id FROM fallback f ORDER BY f.workspace_id LIMIT 1)) AS ws), work AS (
  (SELECT 'event'::text AS source,e.id,e.workspace_id,e.type,e.card_id FROM public.lab_event_delivery d
   JOIN chosen c ON c.ws=d.workspace_id JOIN public.event e ON e.id=d.event_id WHERE d.consumer=$1
   ORDER BY d.event_id LIMIT LEAST(200,GREATEST(0,$3)))
  UNION ALL
  (SELECT 'dirty'::text,d.id,d.workspace_id,'decision.updated'::text,d.card_id FROM public.lab_fact_dirty d
   JOIN chosen c ON c.ws=d.workspace_id WHERE d.consumer=$1 ORDER BY d.id LIMIT LEAST(200,GREATEST(0,$3)))
 ) SELECT * FROM work ORDER BY id,source LIMIT LEAST(200,GREATEST(0,$3))
$$;
CREATE FUNCTION lab_ack_work(consumer text,event_ids bigint[],dirty_ids bigint[]) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF COALESCE(cardinality($2),0)+COALESCE(cardinality($3),0)>200 THEN
  RAISE EXCEPTION 'at most 200 captured work IDs may be acknowledged';
 END IF;
 DELETE FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.event_id=ANY($2);
 DELETE FROM public.lab_fact_dirty d WHERE d.consumer=$1 AND d.id=ANY($3);
END $$;
REVOKE ALL ON FUNCTION lab_pending_workspace_work(text,uuid,int),lab_ack_work(text,bigint[],bigint[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_pending_workspace_work(text,uuid,int),lab_ack_work(text,bigint[],bigint[]) TO lykeion_app;

-- Freeze only server-validated, visible evidence alongside the accepted answer.
ALTER TABLE lab_message ADD COLUMN evidence jsonb NOT NULL DEFAULT '[]',
 ADD COLUMN history_safe boolean,
 ADD COLUMN launch_attempt integer,
 ADD COLUMN stale_attempt integer,
 ADD COLUMN launch_sources uuid[];
-- NULL denotes a legacy or unvalidated message. Historical reuse additionally
-- requires checked evidence and a verified launch-dependency chain.
-- Only the checked answer recorder may publish or change an answer row.
-- Questions and plain settlement failures are the other server write paths.
DROP POLICY owner ON lab_message;
CREATE POLICY owner_view ON lab_message FOR SELECT USING (
 workspace_id=app_workspace() AND owner_id=app_user());
CREATE POLICY owner_append ON lab_message FOR INSERT WITH CHECK (
 workspace_id=app_workspace() AND owner_id=app_user() AND history_safe IS NULL
 AND (kind='question' OR (kind='failure' AND EXISTS (
 SELECT 1 FROM agent_run r WHERE r.workspace_id=lab_message.workspace_id
 AND r.id=lab_message.agent_run_id AND r.lab_chat_id=lab_message.chat_id
 AND r.requester_id=app_user() AND r.kind='aristotle' AND r.status='failed'))));
-- The start transaction records which answer IDs the CLI actually received.
-- Only launch validation fields on a private question can be updated.
CREATE POLICY owner_launch ON lab_message FOR UPDATE USING (
 workspace_id=app_workspace() AND owner_id=app_user() AND kind='question')
 WITH CHECK (workspace_id=app_workspace() AND owner_id=app_user() AND kind='question');
REVOKE UPDATE ON lab_message FROM lykeion_app;
GRANT UPDATE(launch_attempt,launch_sources,stale_attempt) ON lab_message TO lykeion_app;
CREATE UNIQUE INDEX lab_turn_outcome ON lab_message(agent_run_id)
 WHERE agent_run_id IS NOT NULL AND kind IN ('answer','failure');

CREATE FUNCTION lab_record_answer(run uuid, statements jsonb) RETURNS SETOF uuid
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 INSERT INTO public.lab_message(workspace_id,chat_id,owner_id,agent_run_id,kind,body,statements,evidence,history_safe)
 SELECT r.workspace_id,c.id,c.owner_id,r.id,'answer','',$2->'statements',$2->'evidence',COALESCE(($2->>'history_safe')::boolean,false)
 FROM public.agent_run r JOIN public.lab_chat c
 ON (c.workspace_id,c.id,c.owner_id)=(r.workspace_id,r.lab_chat_id,r.requester_id)
 JOIN public.member m ON (m.workspace_id,m.user_id)=(r.workspace_id,r.requester_id)
 WHERE r.id=$1 AND r.workspace_id=public.app_workspace() AND r.requester_id=public.app_user()
 AND r.kind='aristotle' AND r.status='running'
 AND EXISTS (SELECT 1 FROM public.lab_message q WHERE q.workspace_id=r.workspace_id AND q.chat_id=r.lab_chat_id
 AND q.owner_id=r.requester_id AND q.agent_run_id=r.id AND q.kind='question'
 AND q.launch_attempt=r.attempt AND q.launch_sources IS NOT NULL AND q.stale_attempt IS DISTINCT FROM r.attempt)
 ON CONFLICT DO NOTHING RETURNING id
$$;
REVOKE ALL ON FUNCTION lab_record_answer(uuid,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_record_answer(uuid,jsonb) TO lykeion_app;

-- Lease recovery runs as the maintenance identity and needs only the accepted
-- outcome bit, never private message content.
CREATE FUNCTION lab_has_accepted_answer(run uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.lab_message m WHERE m.workspace_id=public.app_workspace()
 AND m.agent_run_id=$1 AND m.kind='answer' AND m.history_safe IS NOT NULL
 AND (m.owner_id=public.app_user() OR public.app_user()='00000000-0000-0000-0000-000000000000'::uuid))
$$;
REVOKE ALL ON FUNCTION lab_has_accepted_answer(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_has_accepted_answer(uuid) TO lykeion_app;

-- Establish the cutover baseline while card, event and decision producers are
-- excluded. Old column changes are already represented by the current card;
-- replaying them against that snapshot would invent moves in reverse.
LOCK TABLE public.card, public.event, public.inbox_item IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO public.lab_event_consumer VALUES ('lessons');
INSERT INTO public.lab_event_delivery(consumer,event_id,workspace_id)
 SELECT 'lessons',id,workspace_id FROM public.event WHERE type='inbox.resolved' AND card_id IS NOT NULL;
ALTER TABLE lesson_card ADD COLUMN board_state text NOT NULL DEFAULT '';
INSERT INTO lesson_card(workspace_id,card_id,board_column,board_state)
 SELECT workspace_id,id,board_column,state FROM card;
CREATE FUNCTION lab_seed_lesson_card() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
 SET search_path=pg_catalog,public AS $$
BEGIN
 INSERT INTO public.lesson_card(workspace_id,card_id,board_column,board_state)
 VALUES(NEW.workspace_id,NEW.id,NEW.board_column,NEW.state);
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION lab_seed_lesson_card() FROM PUBLIC;
CREATE TRIGGER lab_seed_lesson_card AFTER INSERT ON card FOR EACH ROW EXECUTE FUNCTION lab_seed_lesson_card();
CREATE INDEX lesson_active_sweep ON lesson(workspace_id,id) WHERE status='active';
CREATE INDEX lesson_card_queued ON lesson_job(workspace_id,card_id,created_at,id)
 WHERE status='queued' AND batch_id='' AND lesson_id IS NULL;
-- Skip empty workspaces; the global scheduler sees only the next tenant ID.
CREATE FUNCTION lab_next_lesson_workspace(after_id uuid) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(
  (SELECT l.workspace_id FROM public.lesson l WHERE l.status='active' AND l.workspace_id>$1 ORDER BY l.workspace_id LIMIT 1),
  (SELECT l.workspace_id FROM public.lesson l WHERE l.status='active' ORDER BY l.workspace_id LIMIT 1))
$$;
REVOKE ALL ON FUNCTION lab_next_lesson_workspace(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_next_lesson_workspace(uuid) TO lykeion_app;
