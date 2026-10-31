-- name: InsertFieldPack :one
INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, uploaded_by, domain, stage)
VALUES (@workspace_id, @name, @version, @sha256, @manifest, @blob_key, @source, sqlc.narg(uploaded_by), @domain, @stage)
RETURNING *;

-- name: GetFieldPack :one
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetFieldPackAvatar :one
UPDATE field_pack SET avatar_url = @avatar_url WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

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

-- name: SetCardPack :exec
UPDATE card SET pack_id = @pack_id WHERE workspace_id = @workspace_id AND id = @card_id;

-- The harnesses the owner has an online runtime for.
-- name: OnlineHarnesses :many
SELECT DISTINCT harness FROM runtime WHERE workspace_id = @workspace_id AND owner_id = @owner_id AND status = 'online'
ORDER BY harness;

-- The harnesses the owner has a runtime for, online or not.
-- name: OwnerHarnesses :many
SELECT DISTINCT harness FROM runtime WHERE workspace_id = @workspace_id AND owner_id = @owner_id
ORDER BY harness;

-- Whether the name belongs to a shipped pack or to someone other than the user.
-- name: PackNameTaken :one
SELECT EXISTS (SELECT 1 FROM field_pack WHERE workspace_id = @workspace_id AND name = @name
  AND uploaded_by IS DISTINCT FROM @user_id::uuid) AS taken;

-- Saving a Team rewrites its row; the id and the stored version stay.
-- name: ReplaceFieldPack :one
UPDATE field_pack SET sha256 = @sha256, manifest = @manifest, blob_key = @blob_key, domain = @domain, stage = @stage
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- What still needs the Team as it is: unfinished studies with the Team on a
-- column they have not finished (the current one included), and the
-- Autoresearches that name it.
-- name: TeamUses :one
SELECT
  (SELECT count(DISTINCT c.id) FROM card c JOIN card_team t ON t.workspace_id = c.workspace_id AND t.card_id = c.id
   WHERE c.workspace_id = @workspace_id AND t.pack_id = @pack_id AND c.ending = '' AND c.state <> 'done'
     AND array_position(@stages::text[], t.board_column) >= array_position(@stages::text[], c.board_column))::int AS studies,
  (SELECT count(DISTINCT a.autoresearch_id) FROM autoresearch_team a
   WHERE a.workspace_id = @workspace_id AND a.pack_id = @pack_id)::int AS autoresearches;

-- name: AllWorkspaceIDs :many
SELECT id::uuid AS id FROM all_workspace_ids();

-- name: ShippedPackExists :one
SELECT EXISTS (SELECT 1 FROM field_pack WHERE workspace_id = @workspace_id AND name = @name AND source = 'shipped') AS ok;

-- name: PackNameInUse :one
SELECT EXISTS (SELECT 1 FROM field_pack WHERE workspace_id = @workspace_id AND name = @name) AS used;

-- The name moves in the row and in the manifest the row holds.
-- name: RenameFieldPack :exec
UPDATE field_pack SET name = @new_name::text, manifest = jsonb_set(manifest, '{name}', to_jsonb(@new_name::text))
WHERE workspace_id = @workspace_id AND name = @name AND source <> 'shipped';

-- The free Team of a stage: the shipped Team labelled for that stage and no field.
-- name: BuiltInStageTeam :one
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND source = 'shipped' AND stage = @stage AND domain = ''
ORDER BY created_at, id LIMIT 1;

-- name: ListOwnPacks :many
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND uploaded_by = @user_id ORDER BY name, created_at;
