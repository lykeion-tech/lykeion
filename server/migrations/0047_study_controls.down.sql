DROP TRIGGER agent_run_attachment_snapshot ON agent_run;
DROP FUNCTION snapshot_run_attachments();
ALTER TABLE agent_run DROP COLUMN attachment_snapshot;
DROP TABLE study_reaction;
