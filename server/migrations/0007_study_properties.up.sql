CREATE TABLE research (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  name text NOT NULL CHECK (length(btrim(name)) > 0),
  description text NOT NULL DEFAULT '',
  created_by uuid NOT NULL REFERENCES "user",
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id)
);

CREATE TABLE agent (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 64),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 1000),
  instructions text NOT NULL DEFAULT '' CHECK (octet_length(instructions) <= 65536),
  harness text NOT NULL CHECK (harness IN ('claude', 'codex')),
  model text NOT NULL DEFAULT '' CHECK (model ~ '^([A-Za-z0-9][A-Za-z0-9._:/@-]{0,199})?$'),
  visibility text NOT NULL DEFAULT 'workspace' CHECK (visibility IN ('workspace', 'private')),
  owner_id uuid NOT NULL REFERENCES "user",
  archived_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id)
);
CREATE UNIQUE INDEX agent_live_name ON agent (workspace_id, lower(name)) WHERE archived_at IS NULL;
ALTER TABLE research ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON research USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE agent ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON research, agent TO lykeion_app;

ALTER TABLE card
  ADD COLUMN research_id uuid,
  ADD COLUMN assignee_type text,
  ADD COLUMN assignee_id uuid,
  ADD CONSTRAINT card_research_fk FOREIGN KEY (workspace_id, research_id) REFERENCES research (workspace_id, id),
  ADD CONSTRAINT card_assignee_tuple CHECK (
    (assignee_type IS NULL AND assignee_id IS NULL) OR
    (assignee_type IS NOT NULL AND assignee_type IN ('member', 'agent') AND assignee_id IS NOT NULL));
CREATE INDEX card_research ON card (workspace_id, research_id) WHERE research_id IS NOT NULL;

-- A polymorphic responsibility link must still name an entity in this lab.
CREATE FUNCTION check_card_assignee() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.assignee_type = 'member' AND NOT EXISTS (
    SELECT 1 FROM member WHERE workspace_id = NEW.workspace_id AND user_id = NEW.assignee_id
  ) THEN
    RAISE EXCEPTION 'assignee is not a workspace member' USING ERRCODE = '23503';
  ELSIF NEW.assignee_type = 'agent' AND NOT EXISTS (
    SELECT 1 FROM agent WHERE workspace_id = NEW.workspace_id AND id = NEW.assignee_id
  ) THEN
    RAISE EXCEPTION 'assignee is not a workspace agent' USING ERRCODE = '23503';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER card_assignee_workspace BEFORE INSERT OR UPDATE OF workspace_id, assignee_type, assignee_id ON card
  FOR EACH ROW EXECUTE FUNCTION check_card_assignee();
