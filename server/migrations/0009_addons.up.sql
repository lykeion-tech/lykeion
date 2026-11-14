-- A workspace's additions to one column's stage roles: text appended to the skill of
-- every role but the reviewer, and tool servers those roles may call. Only the
-- workspace owner writes it; every member can read it.
CREATE TABLE stage_addon (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  board_column text NOT NULL
    CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  skill_md text NOT NULL DEFAULT '',
  mcp jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(mcp) = 'array'),
  updated_by uuid NOT NULL REFERENCES "user",
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, board_column)
);
ALTER TABLE stage_addon ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON stage_addon USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON stage_addon TO lykeion_app;

-- What an agent run was queued with: the add-on's hash ('' when none applied) and
-- the add-on servers, which the claim checks again and hands to the daemon.
ALTER TABLE agent_run ADD COLUMN addon_hash text NOT NULL DEFAULT '';
ALTER TABLE agent_run ADD COLUMN addon_mcp jsonb NOT NULL DEFAULT '[]';
