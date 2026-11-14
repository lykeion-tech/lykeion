ALTER TABLE card DROP CONSTRAINT card_research_fk;
ALTER TABLE card ADD CONSTRAINT card_research_fk
  FOREIGN KEY (workspace_id, research_id) REFERENCES research (workspace_id, id)
  ON DELETE SET NULL (research_id);
