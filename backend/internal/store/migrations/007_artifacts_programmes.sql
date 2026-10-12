CREATE TABLE uploads (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id),
 user_id text NOT NULL REFERENCES users(id), idempotency_key text NOT NULL,
 sha256 text NOT NULL, byte_length integer NOT NULL CHECK(byte_length BETWEEN 1 AND 65536),
 bytes bytea, complete boolean NOT NULL DEFAULT false,
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '15 minutes',
 UNIQUE(workspace_id,user_id,idempotency_key)
);
CREATE TABLE artifacts (
 id text PRIMARY KEY,workspace_id text NOT NULL REFERENCES workspaces(id),
 author_id text NOT NULL REFERENCES users(id), authored_kind text NOT NULL,
 manifest bytea NOT NULL, sha256 text NOT NULL, sealed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE programme_grants (
 programme_id text NOT NULL REFERENCES programmes(id),user_id text NOT NULL REFERENCES users(id),
 capabilities jsonb NOT NULL,revision bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(programme_id,user_id)
);
CREATE TABLE programme_ceilings (
 programme_id text NOT NULL REFERENCES programmes(id),revision bigint NOT NULL,
 amounts jsonb NOT NULL,author_id text NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(programme_id,revision)
);
