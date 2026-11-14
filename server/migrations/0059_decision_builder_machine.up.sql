ALTER TABLE lab_chat ADD COLUMN builder_runtime_id uuid;
ALTER TABLE lab_chat ADD CONSTRAINT lab_chat_builder_runtime_purpose CHECK (builder_runtime_id IS NULL OR purpose='manual_decision_draft');

CREATE FUNCTION lab_builder_machine_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.builder_runtime_id IS NOT NULL AND NEW.builder_runtime_id IS DISTINCT FROM OLD.builder_runtime_id THEN
  RAISE EXCEPTION 'builder machine is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER lab_builder_machine_immutable BEFORE UPDATE ON lab_chat FOR EACH ROW EXECUTE FUNCTION lab_builder_machine_immutable();

CREATE FUNCTION lab_builder_machine_claim_guard() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NEW.kind='aristotle' AND NEW.status IN ('dispatched','running') AND EXISTS (
  SELECT 1 FROM public.lab_chat c WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.lab_chat_id
   AND c.builder_runtime_id IS NOT NULL AND c.builder_runtime_id IS DISTINCT FROM NEW.runtime_id
 ) THEN
  RAISE EXCEPTION 'builder turn must run on its selected machine';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER lab_builder_machine_claim_guard BEFORE INSERT OR UPDATE ON agent_run FOR EACH ROW EXECUTE FUNCTION lab_builder_machine_claim_guard();
