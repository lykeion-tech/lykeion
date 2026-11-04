ALTER TABLE lab_chat ADD COLUMN purpose text NOT NULL DEFAULT 'answer' CHECK (purpose IN ('answer','manual_decision_draft')),
 ADD COLUMN draft jsonb, ADD COLUMN draft_source uuid;
ALTER TABLE agent_run ADD COLUMN lab_purpose text NOT NULL DEFAULT 'answer' CHECK (lab_purpose IN ('answer','manual_decision_draft'));
ALTER TABLE lab_message ADD COLUMN proposal jsonb, ADD COLUMN request_id uuid;
CREATE UNIQUE INDEX lab_draft_request ON lab_message(chat_id,request_id) WHERE request_id IS NOT NULL;

CREATE FUNCTION lab_purpose_guard() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF TG_TABLE_NAME='lab_chat' THEN
  IF TG_OP='UPDATE' AND (NEW.purpose,NEW.owner_id,NEW.workspace_id) IS DISTINCT FROM (OLD.purpose,OLD.owner_id,OLD.workspace_id) THEN RAISE EXCEPTION 'chat identity is immutable'; END IF;
  IF NEW.deleted_at IS NOT NULL THEN NEW.draft=NULL; NEW.draft_source=NULL; END IF;
 ELSE
  IF TG_OP='UPDATE' AND NEW.lab_purpose IS DISTINCT FROM OLD.lab_purpose THEN RAISE EXCEPTION 'run purpose is immutable'; END IF;
  IF NEW.kind='aristotle' AND EXISTS (SELECT 1 FROM lab_chat c WHERE c.id=NEW.lab_chat_id AND c.workspace_id=NEW.workspace_id AND c.purpose<>NEW.lab_purpose) THEN RAISE EXCEPTION 'run purpose must match chat'; END IF;
  IF NEW.kind<>'aristotle' AND NEW.lab_purpose<>'answer' THEN RAISE EXCEPTION 'draft purpose requires lab run'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER lab_purpose_guard BEFORE INSERT OR UPDATE ON lab_chat FOR EACH ROW EXECUTE FUNCTION lab_purpose_guard();
CREATE TRIGGER lab_purpose_guard BEFORE INSERT OR UPDATE ON agent_run FOR EACH ROW EXECUTE FUNCTION lab_purpose_guard();

CREATE OR REPLACE FUNCTION lab_record_answer(run uuid, statements jsonb) RETURNS SETOF uuid
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 INSERT INTO public.lab_message(workspace_id,chat_id,owner_id,agent_run_id,kind,body,statements,evidence,history_safe,proposal)
 SELECT r.workspace_id,c.id,c.owner_id,r.id,'answer','',$2->'statements',$2->'evidence',COALESCE(($2->>'history_safe')::boolean,false),
 CASE WHEN c.purpose='manual_decision_draft' THEN $2->'proposal' ELSE NULL END
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
GRANT UPDATE(request_id) ON lab_message TO lykeion_app;
