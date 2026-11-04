CREATE OR REPLACE FUNCTION lab_record_answer(run uuid, statements jsonb) RETURNS SETOF uuid
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
DROP TRIGGER lab_purpose_guard ON agent_run;
DROP TRIGGER lab_purpose_guard ON lab_chat;
DROP FUNCTION lab_purpose_guard();
DROP INDEX lab_draft_request;
ALTER TABLE lab_message DROP COLUMN proposal, DROP COLUMN request_id;
ALTER TABLE agent_run DROP COLUMN lab_purpose;
ALTER TABLE lab_chat DROP COLUMN purpose, DROP COLUMN draft, DROP COLUMN draft_source;
