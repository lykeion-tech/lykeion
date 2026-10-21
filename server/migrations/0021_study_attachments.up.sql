-- A file attached to a study. It is uploaded first (card_id NULL) and linked when
-- the study is created; the study's deletion takes its rows with it.
CREATE TABLE attachment (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  uploader_id uuid NOT NULL REFERENCES "user",
  card_id uuid,
  name text NOT NULL,
  disk_name text NOT NULL DEFAULT '',
  content_type text NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 0 AND 104857600),
  sha256 text NOT NULL,
  blob_key text NOT NULL,
  record_key text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX attachment_card ON attachment (workspace_id, card_id);
CREATE INDEX attachment_pending ON attachment (created_at) WHERE card_id IS NULL;

ALTER TABLE attachment ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON attachment USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON attachment TO lykeion_app;

-- Uploads never linked to a study, across every workspace, for the cleanup sweep.
CREATE FUNCTION stale_pending_attachments(before timestamptz)
RETURNS TABLE (id uuid, workspace_id uuid, blob_key text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT a.id, a.workspace_id, a.blob_key FROM attachment a
  WHERE a.card_id IS NULL AND a.created_at < before
$$;
REVOKE ALL ON FUNCTION stale_pending_attachments(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION stale_pending_attachments(timestamptz) TO lykeion_app;
