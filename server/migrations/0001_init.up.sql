CREATE FUNCTION app_workspace() RETURNS uuid LANGUAGE sql STABLE
  AS $$ SELECT NULLIF(current_setting('app.workspace_id', true), '')::uuid $$;
CREATE FUNCTION app_user() RETURNS uuid LANGUAGE sql STABLE
  AS $$ SELECT NULLIF(current_setting('app.user_id', true), '')::uuid $$;
CREATE FUNCTION app_email() RETURNS text LANGUAGE sql STABLE
  AS $$ SELECT NULLIF(current_setting('app.user_email', true), '') $$;

CREATE TABLE "user" (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL UNIQUE,
  name text NOT NULL DEFAULT '',
  google_sub text UNIQUE,
  timezone text NOT NULL DEFAULT 'UTC',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE verification_code (
  email text PRIMARY KEY,
  code_hash text NOT NULL,
  expires_at timestamptz NOT NULL,
  attempts int NOT NULL DEFAULT 0
);

CREATE TABLE personal_access_token (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  token_hash text NOT NULL UNIQUE,
  prefix text NOT NULL,
  name text NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspace (
  id uuid PRIMARY KEY,
  slug text NOT NULL UNIQUE,
  name text NOT NULL,
  settings jsonb NOT NULL DEFAULT '{}',
  card_counter int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE member (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  role text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE workspace_invitation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  email text NOT NULL,
  role text NOT NULL CHECK (role IN ('admin', 'member')),
  token_hash text NOT NULL UNIQUE,
  invited_by uuid NOT NULL REFERENCES "user",
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE card (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  number int NOT NULL,
  title text NOT NULL,
  question text NOT NULL,
  owner_id uuid NOT NULL REFERENCES "user",
  budget_usd double precision NOT NULL DEFAULT 0,
  deadline timestamptz,
  spent_usd double precision NOT NULL DEFAULT 0,
  board_column text NOT NULL DEFAULT 'backlog',
  state text NOT NULL DEFAULT 'working',
  flag text NOT NULL DEFAULT '',
  ending text NOT NULL DEFAULT '',
  end_reason text NOT NULL DEFAULT '',
  rounds int NOT NULL DEFAULT 0,
  route_backs int NOT NULL DEFAULT 0,
  stale text[] NOT NULL DEFAULT '{}',
  rerun text NOT NULL DEFAULT '',
  follows_from uuid[] NOT NULL DEFAULT '{}',
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, number)
);

CREATE TABLE comment (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL REFERENCES card ON DELETE CASCADE,
  author_id uuid NOT NULL REFERENCES "user",
  body text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE inbox_item (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL REFERENCES card ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('escalated', 'out_of_budget', 'released', 'publish_request')),
  text text NOT NULL,
  read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE event (
  id bigserial PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid,
  type text NOT NULL,
  actor_type text NOT NULL CHECK (actor_type IN ('user', 'role', 'system')),
  actor_id uuid,
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_workspace_id ON event (workspace_id, id);

ALTER TABLE workspace ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace
  USING (id = app_workspace() OR EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = workspace.id AND m.user_id = app_user()))
  WITH CHECK (id = app_workspace());

ALTER TABLE member ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON member
  USING (workspace_id = app_workspace() OR user_id = app_user())
  WITH CHECK (workspace_id = app_workspace());

ALTER TABLE workspace_invitation ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workspace_invitation
  USING (workspace_id = app_workspace() OR lower(email) = lower(app_email()))
  WITH CHECK (workspace_id = app_workspace());

ALTER TABLE card ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON card USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE comment ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON comment USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE inbox_item ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON inbox_item USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE event ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON event USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());

DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'lykeion_app') THEN
    CREATE ROLE lykeion_app NOLOGIN;
  END IF;
END $$;
GRANT USAGE ON SCHEMA public TO lykeion_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON "user", verification_code, personal_access_token, workspace, member,
  workspace_invitation, card, comment, inbox_item, event TO lykeion_app;
GRANT USAGE ON SEQUENCE event_id_seq TO lykeion_app;
