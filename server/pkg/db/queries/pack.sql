-- name: InsertFieldPack :one
INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, uploaded_by)
VALUES (@workspace_id, @name, @version, @sha256, @manifest, @blob_key, @source, sqlc.narg(uploaded_by))
RETURNING *;

-- name: GetFieldPack :one
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListFieldPacks :many
SELECT p.*, COALESCE((SELECT array_agg(a.approved_by ORDER BY a.approved_at) FROM field_pack_approval a
  WHERE a.workspace_id = p.workspace_id AND a.pack_id = p.id), '{}')::uuid[] AS approvals
FROM field_pack p WHERE p.workspace_id = @workspace_id ORDER BY p.name, p.created_at;

-- name: ApproveFieldPack :execrows
INSERT INTO field_pack_approval (workspace_id, pack_id, approved_by)
VALUES (@workspace_id, @pack_id, @approved_by) ON CONFLICT DO NOTHING;

-- name: IsFieldPackApprovedBy :one
SELECT EXISTS (SELECT 1 FROM field_pack_approval
  WHERE workspace_id = @workspace_id AND pack_id = @id AND approved_by = @user_id) AS ok;

-- The newest version of a pack name that the user has approved.
-- name: NewestApprovedVersion :one
SELECT p.* FROM field_pack p JOIN field_pack_approval a ON a.workspace_id = p.workspace_id AND a.pack_id = p.id
WHERE p.workspace_id = @workspace_id AND p.name = @name AND a.approved_by = @user_id
ORDER BY p.created_at DESC LIMIT 1;

-- name: SetUserPack :exec
UPDATE "user" SET pack_name = @pack_name WHERE id = @id;

-- name: SetCardPack :exec
UPDATE card SET pack_id = @pack_id WHERE workspace_id = @workspace_id AND id = @card_id;

-- The harnesses the owner has an online runtime for.
-- name: OnlineHarnesses :many
SELECT DISTINCT harness FROM runtime WHERE workspace_id = @workspace_id AND owner_id = @owner_id AND status = 'online'
ORDER BY harness;

-- name: GetFieldPackVersion :one
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND name = @name AND version = @version;

-- Whether the name belongs to a shipped pack or to someone other than the user.
-- name: PackNameTaken :one
SELECT EXISTS (SELECT 1 FROM field_pack WHERE workspace_id = @workspace_id AND name = @name
  AND uploaded_by IS DISTINCT FROM @user_id::uuid) AS taken;

-- The versions of one pack the user may see: shipped ones and their own uploads.
-- name: ListPackVersions :many
SELECT p.*, (a.approved_by IS NOT NULL)::bool AS approvals_by_me, a.approved_at,
  (SELECT count(*) FROM card c WHERE c.workspace_id = p.workspace_id AND c.pack_id = p.id)::int AS pinned_studies
FROM field_pack p LEFT JOIN field_pack_approval a
  ON a.workspace_id = p.workspace_id AND a.pack_id = p.id AND a.approved_by = @user_id
WHERE p.workspace_id = @workspace_id AND p.name = @name AND (p.source = 'shipped' OR p.uploaded_by = @user_id)
ORDER BY p.created_at;

-- name: ListOwnPacks :many
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND uploaded_by = @user_id ORDER BY name, created_at;

-- name: PinNewCard :one
UPDATE card SET pack_id = @pack_id, start_kind = @start_kind, cap = @cap
WHERE workspace_id = @workspace_id AND id = @id RETURNING *;
