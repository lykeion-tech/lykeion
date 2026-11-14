CREATE TABLE workspace_availability (
 workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('skill','mcp')),
 resource_id text NOT NULL CHECK (resource_id <> ''),
 skill_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'skill' AND resource_id NOT LIKE 'bundle:%' THEN resource_id::uuid END) STORED,
 mcp_id uuid GENERATED ALWAYS AS (CASE WHEN kind = 'mcp' THEN resource_id::uuid END) STORED,
 enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL CHECK (revision > 0),
 updated_by uuid NOT NULL REFERENCES "user",
 PRIMARY KEY (workspace_id, kind, resource_id),
 FOREIGN KEY (workspace_id,skill_id) REFERENCES workspace_skill(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id,mcp_id) REFERENCES workspace_mcp_server(workspace_id,id) ON DELETE CASCADE
);
ALTER TABLE workspace_availability ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace_availability USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT,INSERT,UPDATE ON workspace_availability TO lykeion_app;
