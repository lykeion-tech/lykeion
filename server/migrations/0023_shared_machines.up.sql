ALTER TABLE runtime ADD COLUMN visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'workspace'));
CREATE TABLE runtime_share (
 workspace_id uuid NOT NULL,
 runtime_id uuid NOT NULL,
 user_id uuid NOT NULL,
 PRIMARY KEY (runtime_id, user_id),
 FOREIGN KEY (runtime_id, workspace_id) REFERENCES runtime (id, workspace_id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id, user_id) REFERENCES member (workspace_id, user_id) ON DELETE CASCADE
);
ALTER TABLE runtime_share ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON runtime_share USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON runtime_share TO lykeion_app;
ALTER TABLE card ADD COLUMN machine text;
ALTER TABLE agent_run
 ADD COLUMN cancel_reason text CHECK (cancel_reason IN ('runtime_access_revoked')),
 ADD COLUMN machine_name text,
 ADD COLUMN machine_owner_id uuid,
 DROP CONSTRAINT agent_run_failure_reason_check,
 ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline', 'timeout', 'provider_network', 'context_exhausted', 'agent_error', 'runtime_access_denied'));
