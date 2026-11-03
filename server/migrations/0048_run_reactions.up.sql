CREATE TABLE run_reaction (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  run_id uuid NOT NULL REFERENCES agent_run ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  emoji text NOT NULL CHECK (octet_length(emoji) BETWEEN 1 AND 64),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (run_id, user_id, emoji)
);
ALTER TABLE run_reaction ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON run_reaction USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, DELETE ON run_reaction TO lykeion_app;
