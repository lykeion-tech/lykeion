ALTER TABLE research
  ADD COLUMN status text NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'in_progress', 'paused', 'completed', 'cancelled')),
  ADD COLUMN priority text NOT NULL DEFAULT 'none' CHECK (priority IN ('none', 'urgent', 'high', 'medium', 'low')),
  ADD COLUMN lead_type text,
  ADD COLUMN lead_id uuid,
  ADD CONSTRAINT research_lead_tuple CHECK (
    (lead_type IS NULL AND lead_id IS NULL) OR
    (lead_type IS NOT NULL AND lead_type IN ('member', 'agent') AND lead_id IS NOT NULL));

CREATE FUNCTION check_research_lead() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.lead_type = 'member' AND NOT EXISTS (
    SELECT 1 FROM member WHERE workspace_id = NEW.workspace_id AND user_id = NEW.lead_id
  ) THEN
    RAISE EXCEPTION 'lead is not a workspace member' USING ERRCODE = '23503';
  ELSIF NEW.lead_type = 'agent' AND NOT EXISTS (
    SELECT 1 FROM agent WHERE workspace_id = NEW.workspace_id AND id = NEW.lead_id
  ) THEN
    RAISE EXCEPTION 'lead is not a workspace agent' USING ERRCODE = '23503';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER research_lead_workspace BEFORE INSERT OR UPDATE OF workspace_id, lead_type, lead_id ON research
  FOR EACH ROW EXECUTE FUNCTION check_research_lead();
