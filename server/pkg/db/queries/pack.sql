-- name: InsertFieldPack :one
INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, uploaded_by, domain, stage)
VALUES (@workspace_id, @name, @version, @sha256, @manifest, @blob_key, @source, sqlc.narg(uploaded_by), @domain, @stage)
RETURNING *;

-- name: GetFieldPack :one
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetFieldPackAvatar :one
UPDATE field_pack SET avatar_url = @avatar_url WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: ApproveFieldPack :execrows
INSERT INTO field_pack_approval (workspace_id, pack_id, approved_by)
VALUES (@workspace_id, @pack_id, @approved_by) ON CONFLICT DO NOTHING;

-- The harnesses the owner has a runtime for, online or not.
-- name: OwnerHarnesses :many
SELECT DISTINCT harness FROM runtime WHERE workspace_id = @workspace_id AND owner_id = @owner_id
ORDER BY harness;

-- Whether the name belongs to a shipped pack or to someone other than the user.
-- name: PackNameTaken :one
SELECT EXISTS (SELECT 1 FROM field_pack WHERE workspace_id = @workspace_id AND name = @name AND deleted_at IS NULL
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

-- name: ShippedPack :one
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND name = @name AND source = 'shipped';

-- A shipped Team's row follows the pack Lykeion now ships; the id stays, so
-- studies and added agents keep the Team.
-- name: RefreshShippedPack :exec
UPDATE field_pack SET version = @version, sha256 = @sha256, manifest = @manifest, blob_key = @blob_key, domain = @domain, stage = @stage
WHERE workspace_id = @workspace_id AND id = @id AND source = 'shipped';

-- The agents added from a shipped Team's member take its new instructions,
-- unless their owner changed them.
-- name: RefreshTeamAgentInstructions :exec
UPDATE agent a SET instructions = @new_instructions::text, updated_at = now()
FROM team_agent t
WHERE t.workspace_id = @workspace_id AND t.pack_id = @pack_id AND t.member = @member
  AND a.workspace_id = t.workspace_id AND a.id = t.agent_id AND a.instructions = @old_instructions::text;

-- name: PackNameInUse :one
SELECT EXISTS (SELECT 1 FROM field_pack WHERE workspace_id = @workspace_id AND name = @name AND deleted_at IS NULL) AS used;

-- The name moves in the row and in the manifest the row holds.
-- name: RenameFieldPack :exec
UPDATE field_pack SET name = @new_name::text, manifest = jsonb_set(manifest, '{name}', to_jsonb(@new_name::text))
WHERE workspace_id = @workspace_id AND name = @name AND source <> 'shipped' AND deleted_at IS NULL;

-- The free Team of a stage: the shipped Team labelled for that stage and no field.
-- name: BuiltInStageTeam :one
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND source = 'shipped' AND stage = @stage AND domain = ''
ORDER BY created_at, id LIMIT 1;

-- name: ListOwnPacks :many
SELECT * FROM field_pack WHERE workspace_id = @workspace_id AND uploaded_by = @user_id AND deleted_at IS NULL ORDER BY name, created_at;

-- name: UpdateTeamSettings :one
UPDATE field_pack SET name = @name, manifest = @manifest, sha256 = @sha256, blob_key = @blob_key, visibility = @visibility
WHERE workspace_id = @workspace_id AND id = @id RETURNING *;
