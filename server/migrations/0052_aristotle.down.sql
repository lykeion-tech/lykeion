DROP FUNCTION due_lesson_jobs(int), lab_advance(text, bigint), lab_events_after(text, int);
DROP TABLE lab_cursor, lesson_card, lesson_job, lesson_counter, lesson_flag, lesson, lab_read, lab_message;
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_workspace_id_id_key;
DROP POLICY run_read ON lab_chat;
DROP FUNCTION lab_run_can_read(uuid, uuid);
DELETE FROM agent_run WHERE kind = 'aristotle';
ALTER TABLE agent_run
  DROP CONSTRAINT agent_run_kind_shape,
  DROP CONSTRAINT agent_run_kind_check,
  DROP COLUMN lab_chat_id,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch', 'coresearch', 'rerun')),
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NULL)
    OR (kind = 'coresearch' AND card_id IS NULL AND requester_id IS NOT NULL AND coresearch_chat_id IS NOT NULL)
    OR (kind = 'rerun' AND card_id IS NOT NULL));
DROP TABLE lab_chat;
