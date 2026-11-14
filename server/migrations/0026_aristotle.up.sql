ALTER TABLE agent ADD COLUMN built_in_key text NOT NULL DEFAULT '' CHECK (built_in_key IN ('','aristotle')),
 ADD COLUMN conversation_starters jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(conversation_starters)='array'),
 ADD CONSTRAINT agent_builtin_personal CHECK (built_in_key='' OR (visibility='private' AND archived_at IS NULL));
DROP INDEX agent_live_name;
CREATE UNIQUE INDEX agent_live_name ON agent(workspace_id,lower(name)) WHERE archived_at IS NULL AND built_in_key='';
CREATE UNIQUE INDEX agent_builtin_owner ON agent(workspace_id,owner_id,built_in_key) WHERE built_in_key<>'';
ALTER TABLE coresearch_project ADD COLUMN conversation_owner_id uuid REFERENCES "user";
CREATE TABLE coresearch_conversation (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
 owner_id uuid NOT NULL REFERENCES "user", agent_id uuid NOT NULL, project_id uuid NOT NULL, chat_id uuid NOT NULL,
 title text NOT NULL DEFAULT '', is_welcome boolean NOT NULL DEFAULT false,
 snapshot_harness text CHECK(snapshot_harness IN ('claude','codex')), snapshot_model text, snapshot_instructions text,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id), UNIQUE(project_id), UNIQUE(chat_id),
 FOREIGN KEY(workspace_id,owner_id) REFERENCES member(workspace_id,user_id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,agent_id) REFERENCES agent(workspace_id,id),
 FOREIGN KEY(workspace_id,project_id) REFERENCES coresearch_project(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,chat_id) REFERENCES coresearch_chat(workspace_id,id) ON DELETE CASCADE,
 CHECK ((snapshot_harness IS NULL AND snapshot_model IS NULL AND snapshot_instructions IS NULL) OR (snapshot_harness IS NOT NULL AND snapshot_model IS NOT NULL AND snapshot_instructions IS NOT NULL))
);
CREATE UNIQUE INDEX coresearch_welcome_owner ON coresearch_conversation(workspace_id,owner_id) WHERE is_welcome;
ALTER TABLE coresearch_conversation ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_conversation USING(workspace_id=app_workspace() AND owner_id=app_user()) WITH CHECK(workspace_id=app_workspace() AND owner_id=app_user());
GRANT SELECT,INSERT,UPDATE,DELETE ON coresearch_conversation TO lykeion_app;

CREATE OR REPLACE FUNCTION coresearch_can_see(project uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT EXISTS (SELECT 1 FROM public.coresearch_project p WHERE p.id=project AND p.workspace_id=public.app_workspace() AND (p.conversation_owner_id IS NULL OR p.conversation_owner_id=public.app_user() OR EXISTS (SELECT 1 FROM public.agent_run ar JOIN public.coresearch_chat ac ON ac.id=ar.coresearch_chat_id AND ac.workspace_id=ar.workspace_id WHERE ar.id=public.app_user() AND ar.workspace_id=p.workspace_id AND ac.project_id=p.id AND ac.owner_id=p.conversation_owner_id AND ar.kind='coresearch' AND ar.status='running'))) AND (EXISTS (
    SELECT 1 FROM public.coresearch_member m
    WHERE m.workspace_id = public.app_workspace() AND m.project_id = project AND m.user_id = public.app_user()
  ) OR EXISTS (
    SELECT 1 FROM public.agent_run r JOIN public.coresearch_chat c
      ON c.workspace_id = r.workspace_id AND c.id = r.coresearch_chat_id
    WHERE r.workspace_id = public.app_workspace() AND r.id = public.app_user() AND r.kind = 'coresearch' AND r.status = 'running' AND c.project_id = project
      AND EXISTS (SELECT 1 FROM public.coresearch_member m JOIN public.member w ON w.workspace_id=m.workspace_id AND w.user_id=m.user_id WHERE m.workspace_id=r.workspace_id AND m.project_id=c.project_id AND m.user_id=c.owner_id)
  ))
$$;
