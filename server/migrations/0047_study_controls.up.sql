CREATE TABLE study_reaction (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL REFERENCES card ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  emoji text NOT NULL CHECK (octet_length(emoji) BETWEEN 1 AND 64),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (card_id, user_id, emoji)
);
ALTER TABLE study_reaction ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON study_reaction USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, DELETE ON study_reaction TO lykeion_app;

-- A run sees the files present when it is created, even if it waits to be claimed.
ALTER TABLE agent_run ADD COLUMN attachment_snapshot uuid[];
UPDATE agent_run r SET attachment_snapshot = ARRAY(
  SELECT a.id FROM attachment a WHERE a.workspace_id = r.workspace_id AND a.card_id = r.card_id AND a.kind = 'file' ORDER BY a.id
) WHERE r.card_id IS NOT NULL;
CREATE FUNCTION snapshot_run_attachments() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.card_id IS NOT NULL AND NEW.attachment_snapshot IS NULL THEN
    SELECT COALESCE(array_agg(id ORDER BY id), ARRAY[]::uuid[]) INTO NEW.attachment_snapshot
      FROM attachment WHERE workspace_id = NEW.workspace_id AND card_id = NEW.card_id AND kind = 'file';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER agent_run_attachment_snapshot BEFORE INSERT ON agent_run FOR EACH ROW EXECUTE FUNCTION snapshot_run_attachments();
