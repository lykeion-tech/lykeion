-- Downgrade preserves accounting identities. Deleted chats may reappear as
-- empty placeholders under older code; erased conversation content stays erased.
DROP POLICY owner_delete ON lab_read;
DROP POLICY owner_delete ON lab_message;
DROP TRIGGER lab_deleted_run_guard ON agent_run;
DROP TRIGGER lab_deleted_run_guard ON agent_run_message;
DROP TRIGGER lab_deleted_run_guard ON lab_read;
DROP TRIGGER lab_deleted_run_guard ON agent_run_receipt;
DROP TRIGGER lab_deleted_run_guard ON agent_run_settlement;
DROP FUNCTION lab_deleted_run_guard();
DROP TRIGGER lab_deleted_content_guard ON lab_message;
DROP FUNCTION lab_deleted_content_guard();
ALTER TABLE lab_chat DROP COLUMN deleted_at;
