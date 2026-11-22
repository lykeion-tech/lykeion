CREATE TABLE github_connection (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  installation_id bigint NOT NULL,
  account_login text NOT NULL,
  account_type text NOT NULL CHECK (account_type IN ('User', 'Organization')),
  account_avatar_url text NOT NULL DEFAULT '',
  connected_by uuid NOT NULL REFERENCES "user",
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
  import_state text NOT NULL DEFAULT 'pending' CHECK (import_state IN ('pending', 'running', 'done', 'failed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, installation_id)
);
CREATE INDEX github_connection_installation ON github_connection (installation_id);

-- A disconnected installation's pull requests and study links remain visible.
CREATE TABLE github_pull_request (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  installation_id bigint NOT NULL,
  repo_owner text NOT NULL CHECK (repo_owner = lower(repo_owner)),
  repo_name text NOT NULL CHECK (repo_name = lower(repo_name)),
  number int NOT NULL,
  title text NOT NULL,
  state text NOT NULL CHECK (state IN ('open', 'draft', 'merged', 'closed')),
  html_url text NOT NULL,
  branch text NOT NULL DEFAULT '',
  body_keys text[] NOT NULL DEFAULT '{}',
  author_login text NOT NULL DEFAULT '',
  author_avatar_url text NOT NULL DEFAULT '',
  additions int NOT NULL DEFAULT 0,
  deletions int NOT NULL DEFAULT 0,
  changed_files int NOT NULL DEFAULT 0,
  head_sha text NOT NULL DEFAULT '',
  merged_at timestamptz,
  closed_at timestamptz,
  pr_created_at timestamptz NOT NULL,
  pr_updated_at timestamptz NOT NULL,
  ci_state text CHECK (ci_state IN ('passed', 'failed', 'pending')),
  ci_passed int NOT NULL DEFAULT 0,
  ci_total int NOT NULL DEFAULT 0,
  ci_failing text[] NOT NULL DEFAULT '{}',
  mergeability text CHECK (mergeability IN ('mergeable', 'conflicting', 'blocked', 'behind')),
  snapshot_at timestamptz,
  snapshot_stale boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, repo_owner, repo_name, number),
  UNIQUE (workspace_id, id)
);

CREATE TABLE github_pr_link (
  workspace_id uuid NOT NULL,
  card_id uuid NOT NULL,
  pr_id uuid NOT NULL,
  source text NOT NULL CHECK (source IN ('auto', 'manual')),
  matched_key text NOT NULL DEFAULT '',
  linked_by uuid REFERENCES "user" ON DELETE SET NULL,
  linked_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (card_id, pr_id),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, pr_id) REFERENCES github_pull_request (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX github_pr_link_pr ON github_pr_link (workspace_id, pr_id);

CREATE TABLE github_pr_unlink (
  workspace_id uuid NOT NULL,
  card_id uuid NOT NULL,
  pr_id uuid NOT NULL,
  PRIMARY KEY (card_id, pr_id),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, pr_id) REFERENCES github_pull_request (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX github_pr_unlink_pr ON github_pr_unlink (workspace_id, pr_id);

CREATE TABLE github_webhook_delivery (
  id text PRIMARY KEY,
  received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX github_webhook_delivery_received ON github_webhook_delivery (received_at);

ALTER TABLE github_connection ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON github_connection USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE github_pull_request ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON github_pull_request USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE github_pr_link ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON github_pr_link USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE github_pr_unlink ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON github_pr_unlink USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON github_connection, github_pull_request, github_pr_link, github_pr_unlink TO lykeion_app;
GRANT SELECT, INSERT, DELETE ON github_webhook_delivery TO lykeion_app;

CREATE FUNCTION github_installation_workspaces(bigint)
RETURNS TABLE (workspace_id uuid, card_prefix text, slug text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog AS $$
  SELECT c.workspace_id, w.card_prefix, w.slug
  FROM public.github_connection c
  JOIN public.workspace w ON w.id = c.workspace_id
  WHERE c.installation_id = $1 AND c.status = 'active'
$$;
REVOKE ALL ON FUNCTION github_installation_workspaces(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION github_installation_workspaces(bigint) TO lykeion_app;

-- Unsuspend and delete events also need to find suspended connections.
CREATE FUNCTION github_installation_lifecycle_workspaces(bigint)
RETURNS TABLE (workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog AS $$
  SELECT c.workspace_id FROM public.github_connection c WHERE c.installation_id = $1
$$;
REVOKE ALL ON FUNCTION github_installation_lifecycle_workspaces(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION github_installation_lifecycle_workspaces(bigint) TO lykeion_app;

CREATE FUNCTION github_pending_imports()
RETURNS TABLE (workspace_id uuid, installation_id bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog AS $$
  SELECT c.workspace_id, c.installation_id FROM public.github_connection c
  WHERE c.import_state IN ('pending', 'running')
$$;
REVOKE ALL ON FUNCTION github_pending_imports() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION github_pending_imports() TO lykeion_app;
