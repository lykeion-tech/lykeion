CREATE TABLE IF NOT EXISTS users (
 id text PRIMARY KEY, display_name text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS workspaces (
 id text PRIMARY KEY, name text NOT NULL, revision bigint NOT NULL DEFAULT 1,
 auth_epoch bigint NOT NULL DEFAULT 1, event_sequence bigint NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS memberships (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id),
 user_id text NOT NULL REFERENCES users(id), access_role text NOT NULL CHECK(access_role IN ('owner','admin','member','viewer')),
 state text NOT NULL DEFAULT 'active' CHECK(state IN ('active','removed')), revision bigint NOT NULL DEFAULT 1,
 UNIQUE(workspace_id,user_id)
);
CREATE TABLE IF NOT EXISTS login_codes (
 digest text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
 digest text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), csrf_digest text NOT NULL,
 csrf_token text NOT NULL, expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS preferences (
 workspace_id text NOT NULL REFERENCES workspaces(id), user_id text NOT NULL REFERENCES users(id),
 kind text NOT NULL, value jsonb NOT NULL, PRIMARY KEY(workspace_id,user_id,kind)
);
CREATE TABLE IF NOT EXISTS operations (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id), user_id text NOT NULL REFERENCES users(id),
 command_name text NOT NULL, idempotency_key text NOT NULL, request_hash text NOT NULL,
 result jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(workspace_id,user_id,command_name,idempotency_key)
);
CREATE TABLE IF NOT EXISTS events (
 workspace_id text NOT NULL REFERENCES workspaces(id), sequence bigint NOT NULL,
 user_id text REFERENCES users(id), payload jsonb NOT NULL,
 PRIMARY KEY(workspace_id,sequence)
);
CREATE TABLE IF NOT EXISTS programmes (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id), name text NOT NULL,
 objective text NOT NULL, revision bigint NOT NULL DEFAULT 1, profile_ref jsonb NOT NULL,
 policy_refs jsonb NOT NULL, budget_ceiling jsonb NOT NULL
);
