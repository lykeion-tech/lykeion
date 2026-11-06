DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM attachment WHERE comment_id IS NOT NULL) THEN
    RAISE EXCEPTION 'Cannot remove comment attachments while files are linked to comments';
  END IF;
END $$;
CREATE OR REPLACE FUNCTION stale_pending_attachments(before timestamptz)
RETURNS TABLE (id uuid, workspace_id uuid, blob_key text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT a.id, a.workspace_id, a.blob_key FROM attachment a
  WHERE a.card_id IS NULL AND a.created_at < before
$$;
ALTER TABLE comment_reservation DROP COLUMN delivery_channel;
ALTER TABLE attachment DROP COLUMN comment_id;
