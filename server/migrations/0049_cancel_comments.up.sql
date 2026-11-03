CREATE TABLE comment_cancellation (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  comment_id uuid PRIMARY KEY REFERENCES comment ON DELETE CASCADE,
  author_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE comment_cancellation ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON comment_cancellation USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT ON comment_cancellation TO lykeion_app;
