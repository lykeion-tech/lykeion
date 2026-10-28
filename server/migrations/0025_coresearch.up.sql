CREATE TABLE coresearch_project (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  python_version text,
  created_by uuid NOT NULL REFERENCES "user",
  created_at timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id)
);

CREATE TABLE coresearch_member (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  user_id uuid NOT NULL,
  role text NOT NULL CHECK (role IN ('owner', 'member')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, user_id),
  FOREIGN KEY (workspace_id, project_id) REFERENCES coresearch_project (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, user_id) REFERENCES member (workspace_id, user_id) ON DELETE CASCADE
);

CREATE TABLE coresearch_file (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  path text NOT NULL,
  UNIQUE (workspace_id, id),
  UNIQUE (project_id, path),
  FOREIGN KEY (workspace_id, project_id) REFERENCES coresearch_project (workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE coresearch_file_version (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  file_id uuid NOT NULL,
  n int NOT NULL,
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  size bigint NOT NULL,
  blob_key text NOT NULL,
  author_user_id uuid REFERENCES "user" ON DELETE SET NULL,
  agent_run_id uuid,
  run_id uuid,
  overrode int,
  restored_from int,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (file_id, n),
  UNIQUE (workspace_id, file_id, n),
  FOREIGN KEY (workspace_id, file_id) REFERENCES coresearch_file (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, agent_run_id) REFERENCES agent_run (workspace_id, id) ON DELETE SET NULL (agent_run_id),
  FOREIGN KEY (workspace_id, file_id, overrode) REFERENCES coresearch_file_version (workspace_id, file_id, n),
  FOREIGN KEY (workspace_id, file_id, restored_from) REFERENCES coresearch_file_version (workspace_id, file_id, n)
);

CREATE TABLE coresearch_chat (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  owner_id uuid NOT NULL REFERENCES "user",
  mode text NOT NULL CHECK (mode IN ('experiment')),
  cli text NOT NULL CHECK (cli IN ('claude', 'codex')),
  daemon_id text,
  kernel_request text CHECK (kernel_request IN ('interrupt', 'restart')),
  kernel_request_seq bigint NOT NULL DEFAULT 0,
  kernel_state text NOT NULL DEFAULT 'stopped' CHECK (kernel_state IN ('stopped', 'idle', 'busy')),
  kernel_seen_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, project_id) REFERENCES coresearch_project (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE agent_run
  ADD COLUMN coresearch_chat_id uuid,
  ADD CONSTRAINT agent_run_coresearch_chat_fkey FOREIGN KEY (workspace_id, coresearch_chat_id)
    REFERENCES coresearch_chat (workspace_id, id) ON DELETE CASCADE,
  DROP CONSTRAINT agent_run_kind_check,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch')),
  DROP CONSTRAINT agent_run_kind_shape,
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL));

CREATE TABLE coresearch_message (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  chat_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('turn', 'reply', 'failure', 'comment')),
  author_user_id uuid REFERENCES "user" ON DELETE SET NULL,
  agent_run_id uuid,
  body text NOT NULL CHECK (octet_length(body) <= 262144),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, chat_id) REFERENCES coresearch_chat (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, agent_run_id) REFERENCES agent_run (workspace_id, id) ON DELETE SET NULL (agent_run_id)
);
CREATE UNIQUE INDEX coresearch_message_agent_kind ON coresearch_message (agent_run_id, kind) WHERE kind IN ('reply', 'failure');

CREATE TABLE coresearch_cell (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  chat_id uuid NOT NULL,
  seq int NOT NULL,
  author_user_id uuid REFERENCES "user" ON DELETE SET NULL,
  agent_run_id uuid,
  kernel text NOT NULL CHECK (char_length(kernel) BETWEEN 1 AND 128),
  source text NOT NULL CHECK (octet_length(source) <= 262144),
  status text NOT NULL CHECK (status IN ('queued', 'running', 'ok', 'error', 'interrupted', 'cancelled')),
  execution_count int,
  outputs jsonb,
  outputs_blob text,
  created_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  finished_at timestamptz,
  UNIQUE (workspace_id, id),
  UNIQUE (chat_id, seq),
  FOREIGN KEY (workspace_id, chat_id) REFERENCES coresearch_chat (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, agent_run_id) REFERENCES agent_run (workspace_id, id) ON DELETE SET NULL (agent_run_id)
);

CREATE TABLE coresearch_run (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  key text NOT NULL,
  chat_id uuid NOT NULL,
  script_file_id uuid NOT NULL,
  script_n int NOT NULL,
  inputs jsonb NOT NULL,
  seed bigint NOT NULL DEFAULT 0,
  env_sha text,
  status text NOT NULL CHECK (status IN ('queued', 'running', 'ok', 'failed', 'cancelled')),
  facts jsonb,
  rerun_of uuid,
  requested_by uuid NOT NULL REFERENCES "user",
  agent_run_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  UNIQUE (workspace_id, id),
  UNIQUE (project_id, key),
  FOREIGN KEY (workspace_id, project_id) REFERENCES coresearch_project (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, chat_id) REFERENCES coresearch_chat (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, script_file_id, script_n) REFERENCES coresearch_file_version (workspace_id, file_id, n),
  FOREIGN KEY (workspace_id, rerun_of) REFERENCES coresearch_run (workspace_id, id) ON DELETE SET NULL (rerun_of),
  FOREIGN KEY (workspace_id, agent_run_id) REFERENCES agent_run (workspace_id, id) ON DELETE SET NULL (agent_run_id)
);
ALTER TABLE coresearch_file_version ADD CONSTRAINT coresearch_file_version_run_fkey
  FOREIGN KEY (workspace_id, run_id) REFERENCES coresearch_run (workspace_id, id) ON DELETE SET NULL (run_id);

CREATE TABLE coresearch_setup (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  project_id uuid NOT NULL,
  chat_id uuid NOT NULL,
  python text,
  conda text[] NOT NULL DEFAULT '{}',
  status text NOT NULL CHECK (status IN ('queued', 'running', 'ok', 'failed', 'cancelled')),
  log text NOT NULL DEFAULT '',
  requested_by uuid NOT NULL REFERENCES "user",
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, project_id) REFERENCES coresearch_project (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, chat_id) REFERENCES coresearch_chat (workspace_id, id) ON DELETE CASCADE
);

CREATE FUNCTION coresearch_can_see(project uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT EXISTS (
    SELECT 1 FROM public.coresearch_member m
    WHERE m.workspace_id = public.app_workspace() AND m.project_id = project AND m.user_id = public.app_user()
  ) OR EXISTS (
    SELECT 1 FROM public.agent_run r JOIN public.coresearch_chat c
      ON c.workspace_id = r.workspace_id AND c.id = r.coresearch_chat_id
    WHERE r.workspace_id = public.app_workspace() AND r.id = public.app_user() AND r.kind = 'coresearch' AND r.status = 'running' AND c.project_id = project
      AND EXISTS (SELECT 1 FROM public.coresearch_member m JOIN public.member w ON w.workspace_id=m.workspace_id AND w.user_id=m.user_id WHERE m.workspace_id=r.workspace_id AND m.project_id=c.project_id AND m.user_id=c.owner_id)
  )
$$;

CREATE FUNCTION coresearch_chat_project(chat uuid) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT project_id FROM public.coresearch_chat
  WHERE workspace_id = public.app_workspace() AND id = chat AND public.coresearch_can_see(project_id)
$$;

CREATE FUNCTION coresearch_file_project(file uuid) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT project_id FROM public.coresearch_file
  WHERE workspace_id = public.app_workspace() AND id = file AND public.coresearch_can_see(project_id)
$$;

REVOKE ALL ON FUNCTION coresearch_can_see(uuid), coresearch_chat_project(uuid), coresearch_file_project(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION coresearch_can_see(uuid), coresearch_chat_project(uuid), coresearch_file_project(uuid) TO lykeion_app;

ALTER TABLE coresearch_project ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_project USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_project AS RESTRICTIVE USING (coresearch_can_see(id));
ALTER TABLE coresearch_member ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_member USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_member AS RESTRICTIVE USING (coresearch_can_see(project_id));
ALTER TABLE coresearch_file ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_file USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_file AS RESTRICTIVE USING (coresearch_can_see(project_id));
ALTER TABLE coresearch_file_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_file_version USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_file_version AS RESTRICTIVE USING (coresearch_can_see(coresearch_file_project(file_id)));
ALTER TABLE coresearch_chat ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_chat USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_chat AS RESTRICTIVE USING (coresearch_can_see(project_id));
ALTER TABLE coresearch_message ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_message USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_message AS RESTRICTIVE USING (coresearch_can_see(coresearch_chat_project(chat_id)));
ALTER TABLE coresearch_cell ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_cell USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_cell AS RESTRICTIVE USING (coresearch_can_see(coresearch_chat_project(chat_id)));
ALTER TABLE coresearch_run ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_run USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_run AS RESTRICTIVE USING (coresearch_can_see(project_id));
ALTER TABLE coresearch_setup ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_setup USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
CREATE POLICY members ON coresearch_setup AS RESTRICTIVE USING (coresearch_can_see(project_id));
GRANT SELECT, INSERT, UPDATE, DELETE ON coresearch_project, coresearch_member, coresearch_file,
  coresearch_file_version, coresearch_chat, coresearch_message, coresearch_cell, coresearch_run, coresearch_setup TO lykeion_app;

CREATE FUNCTION public.coresearch_create_project(project_name text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE project_id uuid;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM public.member m WHERE m.workspace_id = public.app_workspace() AND m.user_id = public.app_user()) THEN
    RAISE EXCEPTION 'workspace membership required' USING ERRCODE = '42501';
  END IF;
  INSERT INTO public.coresearch_project (workspace_id, name, created_by)
    VALUES (public.app_workspace(), project_name, public.app_user()) RETURNING id INTO project_id;
  INSERT INTO public.coresearch_member (workspace_id, project_id, user_id, role)
    VALUES (public.app_workspace(), project_id, public.app_user(), 'owner');
  RETURN project_id;
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_create_project(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_create_project(text) TO lykeion_app;

CREATE FUNCTION public.coresearch_blob_referenced(sha text) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM public.member m WHERE m.workspace_id = public.app_workspace() AND m.user_id = public.app_user()) AND NOT EXISTS (SELECT 1 FROM public.agent_run r JOIN public.coresearch_chat c ON c.workspace_id=r.workspace_id AND c.id=r.coresearch_chat_id JOIN public.coresearch_member m ON m.workspace_id=c.workspace_id AND m.project_id=c.project_id AND m.user_id=c.owner_id JOIN public.member w ON w.workspace_id=m.workspace_id AND w.user_id=m.user_id WHERE r.workspace_id=public.app_workspace() AND r.id=public.app_user() AND r.kind='coresearch' AND r.status='running') THEN
    RAISE EXCEPTION 'workspace membership required' USING ERRCODE = '42501';
  END IF;
  RETURN EXISTS (SELECT 1 FROM public.coresearch_file_version v WHERE v.workspace_id = public.app_workspace() AND v.sha256 = sha);
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_blob_referenced(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_blob_referenced(text) TO lykeion_app;

CREATE FUNCTION public.coresearch_settle(run_id uuid)
RETURNS TABLE(project_id uuid,chat_id uuid,message_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE r public.agent_run; c public.coresearch_chat; message_kind text; message_body text; inserted uuid;
BEGIN
  IF public.app_user() IS DISTINCT FROM '00000000-0000-0000-0000-000000000000'::uuid THEN
    RAISE EXCEPTION 'maintenance identity required' USING ERRCODE = '42501';
  END IF;
  SELECT a.* INTO r FROM public.agent_run a
    JOIN public.agent_run_settlement s ON s.workspace_id=a.workspace_id AND s.agent_run_id=a.id
    WHERE a.workspace_id=public.app_workspace() AND a.id=run_id AND a.kind='coresearch'
      AND a.status IN ('completed','failed','cancelled');
  IF NOT FOUND THEN RAISE EXCEPTION 'terminal Coresearch settlement required' USING ERRCODE = '42501'; END IF;
  SELECT x.* INTO STRICT c FROM public.coresearch_chat x WHERE x.workspace_id=r.workspace_id AND x.id=r.coresearch_chat_id;
  IF r.status='completed' THEN
    message_kind := 'reply';
    SELECT m.content #>> '{}' INTO message_body FROM public.agent_run_message m WHERE m.workspace_id=r.workspace_id
      AND m.agent_run_id=r.id AND m.attempt=r.attempt AND m.type='text' ORDER BY m.seq DESC LIMIT 1;
    message_body := coalesce(message_body,'(no reply)');
  ELSE
    message_kind := 'failure';
    message_body := coalesce(r.failure_reason,r.status) || CASE WHEN r.error='' THEN '' ELSE ': ' || r.error END;
  END IF;
  IF octet_length(message_body)>262144 THEN message_body:=left(message_body,65535)||'…'; END IF;
  INSERT INTO public.coresearch_message(workspace_id,chat_id,kind,agent_run_id,body)
    VALUES(r.workspace_id,c.id,message_kind,r.id,message_body)
    ON CONFLICT (agent_run_id,kind) WHERE kind IN ('reply','failure') DO NOTHING RETURNING id INTO inserted;
  IF inserted IS NOT NULL THEN RETURN QUERY SELECT c.project_id,c.id,inserted; END IF;
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_settle(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_settle(uuid) TO lykeion_app;

ALTER TABLE agent_run DROP CONSTRAINT agent_run_failure_reason_check,
ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline','timeout','provider_network','context_exhausted','agent_error','runtime_access_denied','coresearch_access_revoked'));

ALTER TABLE coresearch_cell ADD COLUMN failure_reason text, ADD COLUMN claimed_daemon_id text;
ALTER TABLE coresearch_run ADD COLUMN failure_reason text, ADD COLUMN claimed_daemon_id text, ADD COLUMN expected_env_sha text;
ALTER TABLE coresearch_setup ADD COLUMN failure_reason text, ADD COLUMN claimed_daemon_id text;

-- Only membership removal may cancel work hidden from a workspace administrator.
CREATE FUNCTION public.coresearch_revoke(target uuid, project uuid)
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
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_revoke(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_revoke(uuid,uuid) TO lykeion_app;

CREATE FUNCTION public.coresearch_depart(target uuid) RETURNS boolean
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
 RETURN true;
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_depart(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_depart(uuid) TO lykeion_app;

ALTER TABLE coresearch_cell ADD COLUMN claim_token uuid, ADD COLUMN lease_expires_at timestamptz, ADD COLUMN receipt_sha text;

ALTER TABLE coresearch_run ADD COLUMN claim_token uuid, ADD COLUMN lease_expires_at timestamptz, ADD COLUMN receipt_sha text;

ALTER TABLE coresearch_setup ADD COLUMN claim_token uuid, ADD COLUMN lease_expires_at timestamptz, ADD COLUMN receipt_sha text;

CREATE FUNCTION public.coresearch_expired_workspaces() RETURNS SETOF uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF public.app_user() IS DISTINCT FROM '00000000-0000-0000-0000-000000000000'::uuid THEN RAISE EXCEPTION 'maintenance identity required' USING ERRCODE='42501'; END IF;
 RETURN QUERY SELECT workspace_id FROM public.coresearch_cell WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=now())
 UNION SELECT workspace_id FROM public.coresearch_run WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=now())
 UNION SELECT workspace_id FROM public.coresearch_setup WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=now()) ORDER BY 1;
END $$;
REVOKE ALL ON FUNCTION public.coresearch_expired_workspaces() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_expired_workspaces() TO lykeion_app;
CREATE FUNCTION public.coresearch_expire() RETURNS TABLE(kind text,project_id uuid,chat_id uuid,work_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE c public.coresearch_chat; item uuid;
BEGIN
 IF public.app_user()<>'00000000-0000-0000-0000-000000000000'::uuid AND NOT EXISTS(SELECT 1 FROM public.member m WHERE m.workspace_id=public.app_workspace() AND m.user_id=public.app_user()) THEN RAISE EXCEPTION 'workspace authority required' USING ERRCODE='42501'; END IF;
 FOR c IN SELECT x.* FROM public.coresearch_chat x WHERE x.workspace_id=public.app_workspace() AND (public.app_user()='00000000-0000-0000-0000-000000000000'::uuid OR public.coresearch_can_see(x.project_id)) ORDER BY x.project_id,x.id FOR UPDATE LOOP
 FOR item IN UPDATE public.coresearch_cell q SET status='interrupted',failure_reason='runtime_lost',finished_at=now(),outputs='[{"type":"error","ename":"RuntimeLost","evalue":"execution lease expired; code was not replayed"}]'::jsonb WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status='running' AND (q.lease_expires_at IS NULL OR q.lease_expires_at<=now()) RETURNING q.id LOOP
 RETURN QUERY SELECT 'cell'::text,c.project_id,c.id,item; END LOOP;
 FOR item IN UPDATE public.coresearch_run q SET status='failed',failure_reason='runtime_lost',finished_at=now(),facts=jsonb_build_object('error','execution lease expired; code was not replayed') WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status='running' AND (q.lease_expires_at IS NULL OR q.lease_expires_at<=now()) RETURNING q.id LOOP
 RETURN QUERY SELECT 'run'::text,c.project_id,c.id,item; END LOOP;
 FOR item IN UPDATE public.coresearch_setup q SET status='failed',failure_reason='runtime_lost',finished_at=now(),log='execution lease expired; setup was not replayed' WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status='running' AND (q.lease_expires_at IS NULL OR q.lease_expires_at<=now()) RETURNING q.id LOOP
 RETURN QUERY SELECT 'setup'::text,c.project_id,c.id,item; END LOOP;
 END LOOP;
END $$;
REVOKE ALL ON FUNCTION public.coresearch_expire() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_expire() TO lykeion_app;
