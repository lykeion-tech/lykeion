ALTER TABLE lab_chat ADD COLUMN applied_message_id uuid,
 ADD CONSTRAINT lab_chat_applied_purpose CHECK (applied_message_id IS NULL OR purpose='manual_decision_draft');
