CREATE TABLE study_draft_request (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  autoresearch_id uuid PRIMARY KEY REFERENCES autoresearch ON DELETE CASCADE,
  research_id uuid,
  start_date date,
  deadline timestamptz,
  card_id uuid REFERENCES card ON DELETE SET NULL,
  completed boolean NOT NULL DEFAULT false,
  FOREIGN KEY (workspace_id, research_id) REFERENCES research(workspace_id, id) ON DELETE SET NULL (research_id)
);
ALTER TABLE study_draft_request ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON study_draft_request USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE ON study_draft_request TO lykeion_app;
