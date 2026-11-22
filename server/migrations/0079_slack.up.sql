CREATE TABLE slack_installation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL UNIQUE REFERENCES workspace ON DELETE CASCADE,
  app_id text NOT NULL UNIQUE,
  team_id text NOT NULL,
  team_name text NOT NULL DEFAULT '',
  bot_user_id text NOT NULL,
  bot_token_ciphertext bytea NOT NULL,
  app_token_ciphertext bytea NOT NULL,
  drafting_agent_id uuid REFERENCES agent ON DELETE SET NULL,
  installed_by uuid NOT NULL REFERENCES "user",
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, workspace_id)
);

CREATE TABLE slack_binding (
  workspace_id uuid NOT NULL,
  installation_id uuid NOT NULL,
  slack_user_id text NOT NULL,
  user_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (installation_id, slack_user_id),
  FOREIGN KEY (installation_id, workspace_id) REFERENCES slack_installation (id, workspace_id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, user_id) REFERENCES member (workspace_id, user_id) ON DELETE CASCADE
);
CREATE INDEX slack_binding_user ON slack_binding (workspace_id, user_id);

CREATE TABLE slack_link_token (
  workspace_id uuid NOT NULL,
  installation_id uuid NOT NULL,
  slack_user_id text NOT NULL,
  token_hash text PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  CHECK (expires_at <= created_at + interval '15 minutes'),
  FOREIGN KEY (installation_id, workspace_id) REFERENCES slack_installation (id, workspace_id) ON DELETE CASCADE
);

CREATE TABLE slack_chat (
  workspace_id uuid NOT NULL,
  installation_id uuid NOT NULL,
  user_id uuid NOT NULL,
  channel text NOT NULL,
  thread_ts text NOT NULL DEFAULT '',
  lab_chat_id uuid NOT NULL,
  PRIMARY KEY (installation_id, user_id, channel, thread_ts),
  FOREIGN KEY (installation_id, workspace_id) REFERENCES slack_installation (id, workspace_id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, lab_chat_id) REFERENCES lab_chat (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX slack_chat_lab_chat ON slack_chat (workspace_id, lab_chat_id);

CREATE TABLE slack_post (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL,
  installation_id uuid NOT NULL,
  user_id uuid NOT NULL,
  channel text NOT NULL,
  thread_ts text NOT NULL DEFAULT '',
  kind text NOT NULL CHECK (kind IN ('answer', 'draft')),
  agent_run_id uuid,
  autoresearch_id uuid REFERENCES autoresearch ON DELETE SET NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'posted', 'dropped')),
  attempts int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (installation_id, workspace_id) REFERENCES slack_installation (id, workspace_id) ON DELETE CASCADE,
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE SET NULL (agent_run_id)
);
CREATE INDEX slack_post_open ON slack_post (installation_id, created_at) WHERE status IN ('pending', 'sending');

ALTER TABLE slack_installation ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON slack_installation USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE slack_binding ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON slack_binding USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE slack_link_token ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON slack_link_token USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE slack_chat ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON slack_chat USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE slack_post ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON slack_post USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON slack_installation, slack_binding, slack_link_token, slack_chat, slack_post TO lykeion_app;

-- The only cross-tenant read: ids of active installations, so the Slack
-- supervisor can open each one under its own workspace.
CREATE FUNCTION slack_active_installations() RETURNS TABLE (id uuid, workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT i.id, i.workspace_id FROM slack_installation i WHERE i.status = 'active'
$$;
REVOKE ALL ON FUNCTION slack_active_installations() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION slack_active_installations() TO lykeion_app;
