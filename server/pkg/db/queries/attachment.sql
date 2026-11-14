-- name: InsertPendingAttachment :one
INSERT INTO attachment (id, workspace_id, uploader_id, name, content_type, size_bytes, sha256, blob_key, text_sha256, text_size_bytes, kind, inner_files, unpacked_bytes, service_name, service_start, service_client)
VALUES (@id, @workspace_id, @uploader_id, @name, @content_type, @size_bytes, @sha256, @blob_key, @text_sha256, @text_size_bytes, @kind, @inner_files, @unpacked_bytes, sqlc.narg(service_name), sqlc.narg(service_start), sqlc.narg(service_client))
RETURNING *;

-- The uploader's own unlinked uploads among ids.
-- name: PendingAttachments :many
SELECT * FROM attachment
WHERE workspace_id = @workspace_id AND uploader_id = @uploader_id AND card_id IS NULL AND comment_id IS NULL AND id = ANY(@ids::uuid[])
ORDER BY created_at, id;

-- Zero rows means the upload was already linked or is gone.
-- name: LinkAttachment :execrows
UPDATE attachment SET card_id = @card_id, disk_name = @disk_name, record_key = @record_key
WHERE id = @id AND card_id IS NULL AND comment_id IS NULL;

-- A study's files in the order they were attached: by record key, c2 before c10.
-- name: CardAttachments :many
SELECT * FROM attachment WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'file' ORDER BY length(record_key), record_key, id;

-- Every upload of a workspace, linked or pending.
-- name: WorkspaceAttachments :many
SELECT * FROM attachment WHERE workspace_id = @workspace_id;

-- Unlinked uploads of every workspace created before the cutoff.
-- name: StalePendingAttachments :many
SELECT id::uuid AS id, workspace_id::uuid AS workspace_id, blob_key::text AS blob_key FROM stale_pending_attachments(@before::timestamptz);

-- Zero rows means the upload was linked to a study after it was listed.
-- name: DeletePendingAttachment :execrows
DELETE FROM attachment WHERE workspace_id = @workspace_id AND id = @id AND card_id IS NULL AND comment_id IS NULL;

-- name: CardAttachment :one
SELECT * FROM attachment WHERE workspace_id = @workspace_id AND card_id = @card_id AND id = @id;

-- A study's hidden services, ordered by their names.
-- name: CardServices :many
SELECT * FROM attachment WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'service' ORDER BY service_name;

-- The uploader claims a pending service and supplies its complete metadata together.
-- name: LinkService :execrows
UPDATE attachment
SET card_id = @card_id, service_name = sqlc.narg(service_name), service_start = sqlc.narg(service_start),
  service_client = sqlc.narg(service_client)
WHERE workspace_id = @workspace_id AND uploader_id = @uploader_id AND id = @id AND kind = 'service' AND card_id IS NULL AND comment_id IS NULL;

-- name: LinkCommentAttachment :execrows
UPDATE attachment SET comment_id = @comment_id
WHERE workspace_id = @workspace_id AND uploader_id = @uploader_id AND id = @id AND kind = 'file' AND card_id IS NULL AND comment_id IS NULL;

-- name: CommentAttachments :many
SELECT * FROM attachment WHERE workspace_id = @workspace_id AND comment_id = @comment_id ORDER BY created_at, id;

-- name: CommentAttachment :one
SELECT a.* FROM attachment a JOIN comment c ON c.workspace_id=a.workspace_id AND c.id=a.comment_id WHERE a.workspace_id = @workspace_id AND c.card_id = @card_id AND c.id = @comment_id AND a.id = @id AND a.kind='file';

-- name: CardCommentAttachments :many
SELECT a.* FROM attachment a JOIN comment c ON c.workspace_id = a.workspace_id AND c.id = a.comment_id
WHERE a.workspace_id = @workspace_id AND c.card_id = @card_id ORDER BY a.created_at, a.id;

-- name: RunCommentAttachment :one
SELECT a.* FROM attachment a JOIN comment c ON c.workspace_id = a.workspace_id AND c.id = a.comment_id
WHERE a.workspace_id = @workspace_id AND c.card_id = @card_id AND a.id = @id AND a.kind = 'file'
AND NOT EXISTS (SELECT 1 FROM comment_cancellation x WHERE x.workspace_id = c.workspace_id AND x.comment_id = c.id)
AND EXISTS (SELECT 1 FROM comment_reservation r WHERE r.workspace_id = c.workspace_id AND r.comment_id = c.id AND r.agent_run_id = @agent_run_id AND r.attempt = @attempt);
