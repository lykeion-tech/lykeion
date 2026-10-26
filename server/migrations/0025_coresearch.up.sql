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
    WHERE r.workspace_id = public.app_workspace() AND r.id = public.app_user() AND r.status = 'running' AND c.project_id = project
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
  IF NOT EXISTS (SELECT 1 FROM public.member m WHERE m.workspace_id = public.app_workspace() AND m.user_id = public.app_user()) THEN
    RAISE EXCEPTION 'workspace membership required' USING ERRCODE = '42501';
  END IF;
  RETURN EXISTS (SELECT 1 FROM public.coresearch_file_version v WHERE v.workspace_id = public.app_workspace() AND v.sha256 = sha);
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_blob_referenced(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_blob_referenced(text) TO lykeion_app;
