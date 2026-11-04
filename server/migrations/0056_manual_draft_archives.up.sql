ALTER TABLE lab_chat ADD COLUMN archived_at timestamptz,
 ADD CONSTRAINT lab_chat_archive_purpose CHECK (archived_at IS NULL OR purpose='manual_decision_draft');
