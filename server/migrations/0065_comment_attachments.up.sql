ALTER TABLE attachment ADD COLUMN comment_id uuid;
ALTER TABLE attachment ADD CONSTRAINT attachment_comment_fk FOREIGN KEY (workspace_id, comment_id) REFERENCES comment (workspace_id, id) ON DELETE CASCADE;
ALTER TABLE attachment ADD CONSTRAINT attachment_single_owner CHECK (card_id IS NULL OR comment_id IS NULL);
CREATE INDEX attachment_comment ON attachment (workspace_id, comment_id, created_at, id);
CREATE OR REPLACE FUNCTION stale_pending_attachments(before timestamptz)
RETURNS TABLE (id uuid, workspace_id uuid, blob_key text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT a.id, a.workspace_id, a.blob_key FROM attachment a
  WHERE a.card_id IS NULL AND a.comment_id IS NULL AND a.created_at < before
$$;
ALTER TABLE comment_reservation ADD COLUMN delivery_channel text NOT NULL DEFAULT 'next_run' CHECK (delivery_channel IN ('next_run','running_turn','member_result'));
