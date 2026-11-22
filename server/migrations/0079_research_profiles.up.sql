CREATE TABLE research_profile_draft (
  user_id uuid PRIMARY KEY REFERENCES "user" ON DELETE CASCADE,
  profile jsonb NOT NULL CHECK (jsonb_typeof(profile) = 'object')
);

CREATE TABLE research_profile_snapshot (
  user_id uuid PRIMARY KEY REFERENCES research_profile_draft (user_id) ON DELETE CASCADE,
  profile jsonb NOT NULL CHECK (jsonb_typeof(profile) = 'object')
);

CREATE TABLE research_profile_publication (
  workspace_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES research_profile_snapshot (user_id) ON DELETE CASCADE,
  PRIMARY KEY (workspace_id, user_id),
  FOREIGN KEY (workspace_id, user_id) REFERENCES member (workspace_id, user_id) ON DELETE CASCADE
);
CREATE INDEX research_profile_publication_user ON research_profile_publication (user_id, workspace_id);

ALTER TABLE research_profile_draft ENABLE ROW LEVEL SECURITY;
CREATE POLICY research_profile_draft_owner ON research_profile_draft
  USING (user_id = app_user()) WITH CHECK (user_id = app_user());

ALTER TABLE research_profile_publication ENABLE ROW LEVEL SECURITY;
CREATE POLICY research_profile_publication_read ON research_profile_publication FOR SELECT
  USING (user_id = app_user() OR (
    workspace_id = app_workspace() AND EXISTS (
      SELECT 1 FROM member WHERE workspace_id = app_workspace() AND user_id = app_user()
    )
  ));
CREATE POLICY research_profile_publication_share ON research_profile_publication FOR INSERT
  WITH CHECK (user_id = app_user() AND workspace_id = app_workspace() AND EXISTS (
    SELECT 1 FROM member WHERE workspace_id = app_workspace() AND user_id = app_user()
  ));
CREATE POLICY research_profile_publication_unshare ON research_profile_publication FOR DELETE
  USING (user_id = app_user() AND workspace_id = app_workspace() AND EXISTS (
    SELECT 1 FROM member WHERE workspace_id = app_workspace() AND user_id = app_user()
  ));

ALTER TABLE research_profile_snapshot ENABLE ROW LEVEL SECURITY;
CREATE POLICY research_profile_snapshot_owner ON research_profile_snapshot
  USING (user_id = app_user()) WITH CHECK (user_id = app_user());
CREATE POLICY research_profile_snapshot_read ON research_profile_snapshot FOR SELECT
  USING (EXISTS (
    SELECT 1 FROM research_profile_publication
    WHERE workspace_id = app_workspace() AND user_id = research_profile_snapshot.user_id
  ));

GRANT SELECT, INSERT, UPDATE ON research_profile_draft, research_profile_snapshot TO lykeion_app;
GRANT SELECT, INSERT, DELETE ON research_profile_publication TO lykeion_app;
