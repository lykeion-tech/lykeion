ALTER TABLE coresearch_file ADD COLUMN deleted_at timestamptz;
UPDATE coresearch_turn_receipt SET body=jsonb_build_object('Body',body,'Mode','','Template','','Model',NULL,'Attachments',NULL)::text;

ALTER TABLE coresearch_conversation DROP CONSTRAINT coresearch_conversation_project_id_key;

ALTER TABLE coresearch_project
  ADD COLUMN mode text NOT NULL DEFAULT 'experiment' CHECK (mode IN ('experiment','cowrite')),
  ADD UNIQUE (workspace_id,id,mode);
ALTER TABLE coresearch_chat
  DROP CONSTRAINT coresearch_chat_mode_check,
  ADD CONSTRAINT coresearch_chat_mode_check CHECK (mode IN ('experiment','cowrite')),
  DROP CONSTRAINT coresearch_chat_kernel_request_check,
  ADD CONSTRAINT coresearch_chat_kernel_request_check CHECK (kernel_request IN ('interrupt','restart','stop')),
  ADD COLUMN kernel_metrics jsonb NOT NULL DEFAULT '[]',
  ADD CONSTRAINT coresearch_chat_project_mode_fkey FOREIGN KEY (workspace_id,project_id,mode)
    REFERENCES coresearch_project (workspace_id,id,mode) ON DELETE CASCADE;
ALTER TABLE runtime
  ADD COLUMN models jsonb NOT NULL DEFAULT '[]' CHECK (octet_length(models::text) <= 65536),
  ADD COLUMN metrics jsonb;

CREATE TABLE coresearch_doc (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  file_id uuid PRIMARY KEY,
  version bigint NOT NULL DEFAULT 0,
  initial_text text NOT NULL,
  checkpoint_text text NOT NULL,
  checkpoint_version bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id,file_id) REFERENCES coresearch_file (workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE coresearch_doc_step (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  file_id uuid NOT NULL,
  version bigint NOT NULL,
  author_user_id uuid REFERENCES "user",
  agent_run_id uuid,
  client_id text NOT NULL,
  changes jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (file_id,version),
  FOREIGN KEY (workspace_id,file_id) REFERENCES coresearch_file (workspace_id,id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id,agent_run_id) REFERENCES agent_run (workspace_id,id),
  CHECK ((author_user_id IS NULL) <> (agent_run_id IS NULL))
);
CREATE FUNCTION refuse_coresearch_doc_step_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'document steps are append-only' USING ERRCODE='23514';
END $$;
CREATE TRIGGER coresearch_doc_step_immutable BEFORE UPDATE OR DELETE ON coresearch_doc_step
  FOR EACH ROW EXECUTE FUNCTION refuse_coresearch_doc_step_update();

CREATE TABLE coresearch_proposal (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  chat_id uuid,
  agent_run_id uuid,
  restore_of int,
  created_by uuid REFERENCES "user" ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,id),
  FOREIGN KEY (workspace_id,project_id) REFERENCES coresearch_project (workspace_id,id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id,chat_id) REFERENCES coresearch_chat (workspace_id,id) ON DELETE SET NULL (chat_id),
  FOREIGN KEY (workspace_id,agent_run_id) REFERENCES agent_run (workspace_id,id) ON DELETE SET NULL (agent_run_id)
);
CREATE TABLE coresearch_change (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  proposal_id uuid NOT NULL,
  file_id uuid NOT NULL,
  from_pos int NOT NULL,
  to_pos int NOT NULL,
  removed_text text NOT NULL,
  file_action text NOT NULL DEFAULT '' CHECK (file_action IN ('','remove','restore')),
  status text NOT NULL CHECK (status IN ('pending','kept','undone','stale')),
  decided_by uuid REFERENCES "user" ON DELETE SET NULL,
  decided_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,id),
  CHECK (from_pos <= to_pos),
  FOREIGN KEY (workspace_id,proposal_id) REFERENCES coresearch_proposal (workspace_id,id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id,file_id) REFERENCES coresearch_file (workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE coresearch_comment (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  file_id uuid NOT NULL,
  from_pos int NOT NULL,
  to_pos int NOT NULL,
  quote text NOT NULL CHECK (octet_length(quote) <= 4096),
  line int NOT NULL,
  author_user_id uuid NOT NULL REFERENCES "user",
  body text NOT NULL CHECK (octet_length(body) <= 16384),
  mentions uuid[] NOT NULL DEFAULT '{}',
  resolved_by uuid REFERENCES "user" ON DELETE SET NULL,
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,id),
  CHECK (from_pos <= to_pos),
  FOREIGN KEY (workspace_id,file_id) REFERENCES coresearch_file (workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE coresearch_build (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  seq int NOT NULL,
  requested_by uuid NOT NULL REFERENCES "user",
  daemon_id text,
  status text NOT NULL CHECK (status IN ('queued','running','ok','failed','aborted')),
  sources jsonb NOT NULL,
  pdf_blob text,
  log_blob text,
  synctex_blob text,
  pages int,
  failure_reason text,
  claimed_daemon_id text,
  claim_token uuid,
  lease_expires_at timestamptz,
  receipt_sha text,
  created_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  finished_at timestamptz,
  UNIQUE (workspace_id,id),
  UNIQUE (project_id,seq),
  FOREIGN KEY (workspace_id,project_id) REFERENCES coresearch_project (workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE coresearch_version (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  seq int NOT NULL,
  title text NOT NULL,
  by_user_id uuid REFERENCES "user" ON DELETE SET NULL,
  build_id uuid,
  files jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,id),
  UNIQUE (project_id,seq),
  FOREIGN KEY (workspace_id,project_id) REFERENCES coresearch_project (workspace_id,id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id,build_id) REFERENCES coresearch_build (workspace_id,id) ON DELETE SET NULL (build_id)
);

ALTER TABLE coresearch_doc ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_doc USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
CREATE POLICY members ON coresearch_doc AS RESTRICTIVE USING (coresearch_can_see(coresearch_file_project(file_id)));

ALTER TABLE coresearch_doc_step ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_doc_step USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
CREATE POLICY members ON coresearch_doc_step AS RESTRICTIVE USING (coresearch_can_see(coresearch_file_project(file_id)));

ALTER TABLE coresearch_proposal ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_proposal USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
CREATE POLICY members ON coresearch_proposal AS RESTRICTIVE USING (coresearch_can_see(project_id));

ALTER TABLE coresearch_change ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_change USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
CREATE POLICY members ON coresearch_change AS RESTRICTIVE USING (coresearch_can_see(coresearch_file_project(file_id)) AND EXISTS (
    SELECT 1 FROM public.coresearch_proposal p WHERE p.workspace_id=app_workspace()
      AND p.id=proposal_id AND coresearch_can_see(p.project_id)));

ALTER TABLE coresearch_comment ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_comment USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
CREATE POLICY members ON coresearch_comment AS RESTRICTIVE USING (coresearch_can_see(coresearch_file_project(file_id)));

ALTER TABLE coresearch_build ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_build USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
CREATE POLICY members ON coresearch_build AS RESTRICTIVE USING (coresearch_can_see(project_id));

ALTER TABLE coresearch_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_version USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
CREATE POLICY members ON coresearch_version AS RESTRICTIVE USING (coresearch_can_see(project_id));

GRANT SELECT,INSERT,UPDATE,DELETE ON coresearch_doc,coresearch_doc_step,coresearch_proposal,coresearch_change,coresearch_comment,coresearch_build,coresearch_version TO lykeion_app;

CREATE OR REPLACE FUNCTION public.coresearch_revoke(target uuid, project uuid)
RETURNS TABLE(kind text, project_id uuid, chat_id uuid, work_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE c public.coresearch_chat; item uuid;
BEGIN
  IF project IS NULL THEN
    IF NOT EXISTS(SELECT 1 FROM public.member m WHERE m.workspace_id=public.app_workspace() AND m.user_id=public.app_user() AND (m.user_id=target OR m.role IN ('owner','admin'))) THEN
      RAISE EXCEPTION 'workspace membership removal authority required' USING ERRCODE='42501';
    END IF;
  ELSIF NOT EXISTS(SELECT 1 FROM public.coresearch_member m JOIN public.member w ON w.workspace_id=m.workspace_id AND w.user_id=m.user_id WHERE m.workspace_id=public.app_workspace() AND m.project_id=project AND m.user_id=public.app_user() AND m.role='owner') THEN
    RAISE EXCEPTION 'project ownership required' USING ERRCODE='42501';
  END IF;
  FOR c IN SELECT x.* FROM public.coresearch_chat x WHERE x.workspace_id=public.app_workspace() AND x.owner_id=target AND (project IS NULL OR x.project_id=project) ORDER BY x.project_id,x.id FOR UPDATE LOOP
    FOR item IN UPDATE public.coresearch_cell q SET status='cancelled',failure_reason='coresearch_access_revoked',finished_at=now() WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status IN ('queued','running') RETURNING q.id LOOP
      RETURN QUERY SELECT 'cell'::text,c.project_id,c.id,item;
    END LOOP;
    FOR item IN UPDATE public.coresearch_run q SET status='cancelled',failure_reason='coresearch_access_revoked',finished_at=now() WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status IN ('queued','running') RETURNING q.id LOOP
      RETURN QUERY SELECT 'run'::text,c.project_id,c.id,item;
    END LOOP;
    FOR item IN UPDATE public.coresearch_setup q SET status='cancelled',failure_reason='coresearch_access_revoked',finished_at=now() WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status IN ('queued','running') RETURNING q.id LOOP
      RETURN QUERY SELECT 'setup'::text,c.project_id,c.id,item;
    END LOOP;
    UPDATE public.coresearch_chat x SET kernel_request=NULL,kernel_state='stopped' WHERE x.workspace_id=c.workspace_id AND x.id=c.id;
    RETURN QUERY SELECT 'chat'::text,c.project_id,c.id,c.id;
    FOR item IN SELECT a.id FROM public.agent_run a WHERE a.workspace_id=c.workspace_id AND a.coresearch_chat_id=c.id AND a.kind='coresearch' AND a.status IN ('queued','dispatched','running') FOR UPDATE LOOP
      RETURN QUERY SELECT 'turn'::text,c.project_id,c.id,item;
    END LOOP;
  END LOOP;
  FOR item IN UPDATE public.coresearch_build b SET status='aborted',failure_reason='coresearch_access_revoked',finished_at=now()
    WHERE b.workspace_id=public.app_workspace() AND b.requested_by=target
      AND (project IS NULL OR b.project_id=project) AND b.status IN ('queued','running') RETURNING b.id LOOP
    RETURN QUERY SELECT 'build'::text,b.project_id,NULL::uuid,b.id FROM public.coresearch_build b WHERE b.id=item;
  END LOOP;
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_revoke(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_revoke(uuid,uuid) TO lykeion_app;

CREATE OR REPLACE FUNCTION public.coresearch_depart(target uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE p public.coresearch_project; successor uuid;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM public.member m WHERE m.workspace_id=public.app_workspace() AND m.user_id=public.app_user() AND (m.user_id=target OR m.role IN ('owner','admin'))) THEN
  RAISE EXCEPTION 'workspace membership removal authority required' USING ERRCODE='42501';
 END IF;
 FOR p IN SELECT x.* FROM public.coresearch_project x JOIN public.coresearch_member m ON m.workspace_id=x.workspace_id AND m.project_id=x.id
 WHERE x.workspace_id=public.app_workspace() AND m.user_id=target ORDER BY x.id FOR UPDATE OF x LOOP
  successor := NULL;
  SELECT m.user_id INTO successor FROM public.coresearch_member m WHERE m.workspace_id=p.workspace_id AND m.project_id=p.id AND m.user_id<>target ORDER BY m.created_at,m.user_id LIMIT 1;
  IF successor IS NULL THEN
   UPDATE public.coresearch_project SET archived_at=coalesce(archived_at,now()),updated_at=now() WHERE workspace_id=p.workspace_id AND id=p.id;
  ELSIF NOT EXISTS(SELECT 1 FROM public.coresearch_member m WHERE m.workspace_id=p.workspace_id AND m.project_id=p.id AND m.user_id<>target AND m.role='owner') THEN
   IF public.app_user()=target THEN RETURN false; END IF;
   UPDATE public.coresearch_member SET role='owner' WHERE workspace_id=p.workspace_id AND project_id=p.id AND user_id=successor;
  END IF;
 END LOOP;
 UPDATE public.coresearch_build b SET status='aborted',failure_reason='coresearch_access_revoked',finished_at=now()
 WHERE b.workspace_id=public.app_workspace() AND b.requested_by=target AND b.status IN ('queued','running');
 RETURN true;
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_depart(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_depart(uuid) TO lykeion_app;

