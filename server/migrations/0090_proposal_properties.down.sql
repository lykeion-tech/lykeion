DROP TRIGGER clear_discarded_proposal_properties ON study_proposal;
DROP FUNCTION clear_discarded_proposal_properties();
ALTER TABLE study_proposal DROP COLUMN title, DROP COLUMN priority;
