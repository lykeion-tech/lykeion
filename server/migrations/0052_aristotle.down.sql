DROP FUNCTION lab_record_answer(uuid,jsonb);
DROP INDEX lab_study_record_links, lab_study_comment_links, lab_study_decision_links;
DROP TRIGGER lab_decision_changed ON inbox_item;
DROP TRIGGER lab_enqueue_event ON event;
DROP FUNCTION lab_decision_changed(), lab_enqueue_event(),
  lab_pending_workspace_work(text,uuid,int), lab_ack_work(text,bigint[],bigint[]),
  lab_pending_workspace_events(text,uuid,int), lab_pending_events(text,int), lab_ack_event(text,bigint),
  lab_index_workspaces_after(uuid,int), lab_index_workspace_exists(uuid);
DROP TABLE lab_fact_dirty, lab_event_delivery, lab_event_consumer;
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
