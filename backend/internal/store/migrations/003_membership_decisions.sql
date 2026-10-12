CREATE TABLE membership_decisions (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id),
 requester_id text NOT NULL REFERENCES users(id), responder_id text NOT NULL REFERENCES users(id),
 member_id text NOT NULL REFERENCES memberships(id), member_revision bigint NOT NULL,
 workspace_revision bigint NOT NULL, action text NOT NULL CHECK(action IN ('change-access','transfer-owner')),
 requested_role text NOT NULL CHECK(requested_role IN ('owner','admin','member','viewer')),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','approved','rejected')),
 revision bigint NOT NULL DEFAULT 1, expires_at timestamptz NOT NULL,
 response jsonb, created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
