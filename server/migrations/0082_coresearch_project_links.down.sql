DROP INDEX coresearch_conversation_research;

ALTER TABLE coresearch_conversation
  DROP COLUMN archived_at,
  DROP COLUMN pinned_at,
  DROP COLUMN research_id;
