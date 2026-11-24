CREATE TABLE mobile_session (
 token_hash text PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
);
CREATE INDEX mobile_session_user_expiry ON mobile_session(user_id, expires_at);
GRANT SELECT, INSERT, DELETE ON mobile_session TO lykeion_app;
