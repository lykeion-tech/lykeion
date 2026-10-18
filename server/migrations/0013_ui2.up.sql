CREATE TABLE workspace_skill (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  name text NOT NULL CHECK (name <> ''),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_by uuid NOT NULL REFERENCES "user",
  source_kind text NOT NULL DEFAULT '',
  source_location text NOT NULL DEFAULT '',
  source_runtime_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, source_runtime_id) REFERENCES runtime (workspace_id, id) ON DELETE SET NULL (source_runtime_id)
);
CREATE UNIQUE INDEX workspace_skill_active_name ON workspace_skill (workspace_id, name) WHERE deleted_at IS NULL;

CREATE TABLE workspace_skill_version (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  skill_id uuid NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  name text NOT NULL CHECK (name <> ''),
  description text NOT NULL,
  domain text NOT NULL DEFAULT 'Workspace' CHECK (domain = 'Workspace'),
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
  blob_key text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, skill_id, revision),
  FOREIGN KEY (workspace_id, skill_id) REFERENCES workspace_skill (workspace_id, id) ON DELETE CASCADE,
  CHECK (blob_key = workspace_id::text || '/skills/' || sha256)
);
ALTER TABLE workspace_skill ADD CONSTRAINT workspace_skill_current_version_fkey
  FOREIGN KEY (workspace_id, id, revision) REFERENCES workspace_skill_version (workspace_id, skill_id, revision)
  DEFERRABLE INITIALLY DEFERRED;

-- Scope-derived columns let ordinary tenant foreign keys check both scope kinds.
ALTER TABLE autoresearch_run ADD CONSTRAINT autoresearch_run_workspace_id_key UNIQUE (workspace_id, id);
CREATE TABLE workspace_skill_pin (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  scope_kind text NOT NULL CHECK (scope_kind IN ('card', 'autoresearch_run')),
  scope_id uuid NOT NULL,
  skill_id uuid NOT NULL,
  revision bigint NOT NULL,
  card_id uuid GENERATED ALWAYS AS (CASE WHEN scope_kind = 'card' THEN scope_id END) STORED,
  autoresearch_run_id uuid GENERATED ALWAYS AS (CASE WHEN scope_kind = 'autoresearch_run' THEN scope_id END) STORED,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, scope_kind, scope_id, skill_id),
  FOREIGN KEY (workspace_id, skill_id, revision) REFERENCES workspace_skill_version (workspace_id, skill_id, revision),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, autoresearch_run_id) REFERENCES autoresearch_run (workspace_id, id) ON DELETE CASCADE
);

-- Saved revisions and pins are append-only; metadata edits produce new versions.
CREATE FUNCTION refuse_workspace_skill_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'skill versions and pins are immutable' USING ERRCODE = '23514';
END $$;
CREATE TRIGGER workspace_skill_version_immutable BEFORE UPDATE ON workspace_skill_version
  FOR EACH ROW EXECUTE FUNCTION refuse_workspace_skill_update();
CREATE TRIGGER workspace_skill_pin_immutable BEFORE UPDATE ON workspace_skill_pin
  FOR EACH ROW EXECUTE FUNCTION refuse_workspace_skill_update();

CREATE TABLE workspace_mcp_server (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  name text NOT NULL CHECK (name <> ''),
  transport text NOT NULL CHECK (transport IN ('url', 'command')),
  config_ciphertext bytea NOT NULL CHECK (octet_length(config_ciphertext) >= 29),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_by uuid NOT NULL REFERENCES "user",
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, name)
);

-- The runtime identity binds acquisition and completion to its owner and daemon.
ALTER TABLE runtime ADD CONSTRAINT runtime_discovery_identity_key UNIQUE (workspace_id, id, owner_id, daemon_id);
CREATE TABLE runtime_skill_request (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  runtime_id uuid NOT NULL,
  owner_id uuid NOT NULL REFERENCES "user",
  daemon_id text NOT NULL,
  operation text NOT NULL CHECK (operation IN ('list', 'import')),
  keys text[] NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed', 'timeout')),
  acquisition_id uuid,
  expires_at timestamptz NOT NULL DEFAULT now() + interval '3 minutes',
  acquired_at timestamptz,
  completed_at timestamptz,
  result jsonb NOT NULL DEFAULT '{}',
  result_blob_key text NOT NULL DEFAULT '',
  error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, runtime_id, owner_id, daemon_id) REFERENCES runtime (workspace_id, id, owner_id, daemon_id) ON DELETE CASCADE,
  CHECK ((operation = 'list' AND cardinality(keys) = 0) OR (operation = 'import' AND cardinality(keys) BETWEEN 1 AND 10)),
  CHECK ((acquisition_id IS NULL) = (acquired_at IS NULL)),
  CHECK (status <> 'running' OR acquisition_id IS NOT NULL),
  CHECK ((status IN ('completed', 'failed', 'timeout')) = (completed_at IS NOT NULL))
);
CREATE INDEX runtime_skill_request_pending ON runtime_skill_request (workspace_id, runtime_id, created_at) WHERE status = 'pending';
ALTER TABLE workspace_connector ADD COLUMN revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0);
CREATE FUNCTION advance_workspace_connector_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.revision := OLD.revision + 1;
  RETURN NEW;
END $$;
CREATE TRIGGER workspace_connector_revision BEFORE UPDATE ON workspace_connector
  FOR EACH ROW EXECUTE FUNCTION advance_workspace_connector_revision();

ALTER TABLE workspace_skill ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace_skill USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE workspace_skill_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace_skill_version USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE workspace_skill_pin ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace_skill_pin USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE workspace_mcp_server ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace_mcp_server USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE runtime_skill_request ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON runtime_skill_request USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON workspace_mcp_server, runtime_skill_request TO lykeion_app;
GRANT SELECT, INSERT, UPDATE ON workspace_skill TO lykeion_app;
GRANT SELECT, INSERT ON workspace_skill_version, workspace_skill_pin TO lykeion_app;
