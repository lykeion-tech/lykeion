ALTER TABLE users ADD COLUMN account_revision bigint NOT NULL DEFAULT 1;
CREATE TABLE invitations (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id),
 recipient_id text NOT NULL REFERENCES users(id), created_by text NOT NULL REFERENCES users(id),
 requested_role text NOT NULL CHECK(requested_role IN ('admin','member','viewer')),
 proof_digest text NOT NULL, expires_at timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','accepted','revoked')),
 revision bigint NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX one_pending_invitation ON invitations(workspace_id,recipient_id) WHERE state='pending';
