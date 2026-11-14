DO $$
BEGIN
 LOCK TABLE lab_chat IN ACCESS EXCLUSIVE MODE;
 IF EXISTS (SELECT 1 FROM lab_chat WHERE builder_runtime_id IS NOT NULL) THEN
  RAISE EXCEPTION 'Cannot remove Decision builder machine binding while bound drafts exist';
 END IF;
 DROP TRIGGER lab_builder_machine_claim_guard ON agent_run;
 DROP FUNCTION lab_builder_machine_claim_guard();
 DROP TRIGGER lab_builder_machine_immutable ON lab_chat;
 DROP FUNCTION lab_builder_machine_immutable();
 ALTER TABLE lab_chat DROP CONSTRAINT lab_chat_builder_runtime_purpose;
 ALTER TABLE lab_chat DROP COLUMN builder_runtime_id;
END $$;
