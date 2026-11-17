-- A person's own TypeSafe key, sealed under the server's LYK_SECRET_KEY.
-- Kept apart from "user" so that rows read with SELECT * never carry it.
CREATE TABLE user_typesafe_key (
  user_id uuid PRIMARY KEY REFERENCES "user" ON DELETE CASCADE,
  ciphertext bytea NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON user_typesafe_key TO lykeion_app;
