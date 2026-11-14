CREATE TABLE agent_capabilities (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_id uuid NOT NULL,
  skills jsonb NOT NULL DEFAULT '[]'::jsonb,
  connectors jsonb NOT NULL DEFAULT '[]'::jsonb,
  PRIMARY KEY (workspace_id, agent_id),
  FOREIGN KEY (workspace_id, agent_id) REFERENCES agent (workspace_id, id) ON DELETE CASCADE
);
ALTER TABLE agent_capabilities ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_capabilities USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_capabilities TO lykeion_app;
