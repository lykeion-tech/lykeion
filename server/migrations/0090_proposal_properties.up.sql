ALTER TABLE study_proposal ADD COLUMN title text, ADD COLUMN priority text;
CREATE FUNCTION clear_discarded_proposal_properties() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.title := NULL;
  NEW.priority := NULL;
  RETURN NEW;
END $$;
CREATE TRIGGER clear_discarded_proposal_properties BEFORE UPDATE ON study_proposal
  FOR EACH ROW WHEN (NEW.discarded_at IS NOT NULL)
  EXECUTE FUNCTION clear_discarded_proposal_properties();
