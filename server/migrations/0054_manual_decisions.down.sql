DROP TRIGGER lab_enqueue_event ON event;
CREATE TRIGGER lab_enqueue_event AFTER INSERT ON event FOR EACH ROW EXECUTE FUNCTION lab_enqueue_event();
DELETE FROM event WHERE recipient_id IS NOT NULL;
DELETE FROM inbox_item WHERE kind='manual_decision';
DROP TRIGGER preserve_event_recipient ON event;
DROP FUNCTION preserve_event_recipient();
DROP POLICY tenant ON event;
CREATE POLICY tenant ON event USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
ALTER TABLE event DROP COLUMN recipient_id;
DROP POLICY tenant ON inbox_item;
CREATE POLICY tenant ON inbox_item USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
DROP TRIGGER preserve_manual_decision_identity ON inbox_item;
DROP FUNCTION preserve_manual_decision_identity();
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_manual_shape, DROP CONSTRAINT inbox_item_kind_check,
 ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
 ('autoresearch_card','autoresearch_report','middle_score','blocked','no_tool','scoring_unavailable','done','waiting_for_machine','needs_decision','open_doubts','agent_decision','machine_offline')),
 DROP COLUMN title,DROP COLUMN context,DROP COLUMN deadline;
