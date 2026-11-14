-- Run identities remain available for daemon cancellation and accounting.
ALTER TABLE lab_chat ADD COLUMN deleted_at timestamptz;

CREATE FUNCTION lab_deleted_content_guard() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM public.lab_chat c WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.chat_id AND c.deleted_at IS NOT NULL) THEN
  RETURN NULL;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER lab_deleted_content_guard BEFORE INSERT OR UPDATE ON lab_message
 FOR EACH ROW EXECUTE FUNCTION lab_deleted_content_guard();

-- Late daemon reports may settle usage, but cannot restore deleted text.
CREATE FUNCTION lab_deleted_run_guard() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE deleted boolean;
BEGIN
 IF TG_TABLE_NAME='agent_run' THEN
  SELECT EXISTS(SELECT 1 FROM public.lab_chat c WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.lab_chat_id AND c.deleted_at IS NOT NULL) INTO deleted;
  IF deleted THEN
   NEW.prompt=''; NEW.skill=''; NEW.error=''; NEW.handoff_note=''; NEW.session_id=''; NEW.input_outcomes='{}';
  END IF;
 ELSE
  SELECT EXISTS(SELECT 1 FROM public.agent_run r JOIN public.lab_chat c ON c.workspace_id=r.workspace_id AND c.id=r.lab_chat_id WHERE r.workspace_id=NEW.workspace_id AND r.id=NEW.agent_run_id AND c.deleted_at IS NOT NULL) INTO deleted;
  IF deleted THEN
   IF TG_TABLE_NAME='agent_run_message' OR TG_TABLE_NAME='lab_read' THEN RETURN NULL; END IF;
   NEW.result=NEW.result - ARRAY['prompt','skill','error','handoff_note','session_id','input_outcomes'];
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER lab_deleted_run_guard BEFORE INSERT OR UPDATE ON agent_run FOR EACH ROW EXECUTE FUNCTION lab_deleted_run_guard();
CREATE TRIGGER lab_deleted_run_guard BEFORE INSERT OR UPDATE ON agent_run_message FOR EACH ROW EXECUTE FUNCTION lab_deleted_run_guard();
CREATE TRIGGER lab_deleted_run_guard BEFORE INSERT OR UPDATE ON lab_read FOR EACH ROW EXECUTE FUNCTION lab_deleted_run_guard();
CREATE TRIGGER lab_deleted_run_guard BEFORE INSERT OR UPDATE ON agent_run_receipt FOR EACH ROW EXECUTE FUNCTION lab_deleted_run_guard();
CREATE TRIGGER lab_deleted_run_guard BEFORE INSERT OR UPDATE ON agent_run_settlement FOR EACH ROW EXECUTE FUNCTION lab_deleted_run_guard();

CREATE POLICY owner_delete ON lab_message FOR DELETE USING (
 workspace_id=app_workspace() AND owner_id=app_user()
 AND EXISTS(SELECT 1 FROM public.lab_chat c WHERE c.workspace_id=lab_message.workspace_id AND c.id=lab_message.chat_id AND c.owner_id=app_user() AND c.deleted_at IS NOT NULL));

-- Existing traversal trails are private conversation content too. Only the
-- current owner may erase them, and only after that chat has been tombstoned.
CREATE POLICY owner_delete ON lab_read FOR DELETE USING (
 workspace_id=app_workspace() AND EXISTS (
 SELECT 1 FROM public.agent_run r
 JOIN public.lab_chat c ON (c.workspace_id,c.id,c.owner_id)=(r.workspace_id,r.lab_chat_id,r.requester_id)
 JOIN public.member m ON (m.workspace_id,m.user_id)=(c.workspace_id,c.owner_id)
 WHERE r.workspace_id=lab_read.workspace_id AND r.id=lab_read.agent_run_id
 AND r.kind='aristotle' AND r.requester_id=app_user() AND c.deleted_at IS NOT NULL));
