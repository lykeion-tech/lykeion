CREATE TABLE coresearch_run_connector (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_run_id uuid NOT NULL,
  connector_id uuid NOT NULL,
  PRIMARY KEY (workspace_id, agent_run_id, connector_id),
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);
ALTER TABLE coresearch_run_connector ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_run_connector USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, DELETE ON coresearch_run_connector TO lykeion_app;
