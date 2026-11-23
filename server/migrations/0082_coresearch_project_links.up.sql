ALTER TABLE coresearch_conversation
  ADD COLUMN research_id uuid,
  ADD COLUMN pinned_at timestamptz,
  ADD COLUMN archived_at timestamptz,
  ADD CONSTRAINT coresearch_conversation_research_fk
    FOREIGN KEY (workspace_id, research_id) REFERENCES research (workspace_id, id)
    ON DELETE SET NULL (research_id);

CREATE INDEX coresearch_conversation_research
  ON coresearch_conversation (workspace_id, research_id)
  WHERE research_id IS NOT NULL;
