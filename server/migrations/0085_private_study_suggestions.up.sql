CREATE OR REPLACE FUNCTION lab_record_answer(run uuid, statements jsonb) RETURNS SETOF uuid
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 INSERT INTO public.lab_message(workspace_id,chat_id,owner_id,agent_run_id,kind,body,statements,evidence,history_safe,proposal)
 SELECT r.workspace_id,c.id,c.owner_id,r.id,'answer','',$2->'statements',$2->'evidence',COALESCE(($2->>'history_safe')::boolean,false),
 CASE WHEN c.purpose='manual_decision_draft' THEN $2->'proposal' WHEN jsonb_typeof($2->'new_study')='object' THEN jsonb_build_object('new_study',$2->'new_study') END
 FROM public.agent_run r JOIN public.lab_chat c
 ON (c.workspace_id,c.id,c.owner_id)=(r.workspace_id,r.lab_chat_id,r.requester_id)
 JOIN public.member m ON (m.workspace_id,m.user_id)=(r.workspace_id,r.requester_id)
 WHERE r.id=$1 AND r.workspace_id=public.app_workspace() AND r.requester_id=public.app_user()
 AND c.deleted_at IS NULL AND r.kind='aristotle' AND r.status='running' AND r.lab_purpose=c.purpose
 AND ((c.purpose='answer' AND NOT ($2 ? 'proposal')) OR (c.purpose='manual_decision_draft' AND jsonb_typeof($2->'proposal')='object'))
 AND EXISTS (SELECT 1 FROM public.lab_message q WHERE q.workspace_id=r.workspace_id AND q.chat_id=r.lab_chat_id
 AND q.owner_id=r.requester_id AND q.agent_run_id=r.id AND q.kind='question'
 AND q.launch_attempt=r.attempt AND q.launch_sources IS NOT NULL AND q.stale_attempt IS DISTINCT FROM r.attempt)
 ON CONFLICT DO NOTHING RETURNING id
$$;
