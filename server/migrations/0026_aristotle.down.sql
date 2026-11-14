CREATE OR REPLACE FUNCTION coresearch_can_see(project uuid) RETURNS boolean
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
DROP TABLE coresearch_conversation;
ALTER TABLE coresearch_project DROP COLUMN conversation_owner_id;
DELETE FROM agent WHERE built_in_key <> '';
DROP INDEX agent_builtin_owner;
DROP INDEX agent_live_name;
ALTER TABLE agent DROP CONSTRAINT agent_builtin_personal, DROP COLUMN built_in_key, DROP COLUMN conversation_starters;
CREATE UNIQUE INDEX agent_live_name ON agent (workspace_id,lower(name)) WHERE archived_at IS NULL;
