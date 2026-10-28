-- name: CreateAgent :one
INSERT INTO agent (workspace_id, name, description, instructions, harness, model, visibility, owner_id, conversation_starters)
VALUES (@workspace_id, @name, @description, @instructions, @harness, @model, @visibility, @owner_id, coalesce(sqlc.narg(conversation_starters)::jsonb, '[]'::jsonb)) RETURNING *;

-- name: ListAgents :many
SELECT * FROM agent WHERE workspace_id = @workspace_id
AND (visibility = 'workspace' OR owner_id = @user_id OR @is_admin::boolean)
ORDER BY created_at, id;

-- name: GetAgent :one
SELECT * FROM agent WHERE workspace_id = @workspace_id AND id = @id;

-- name: UpdateAgent :one
UPDATE agent SET name = @name, description = @description, instructions = @instructions, harness = @harness, model = @model, visibility = @visibility, conversation_starters = @conversation_starters, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND archived_at IS NULL RETURNING *;

-- name: ArchiveAgent :one
UPDATE agent SET archived_at = now(), updated_at = now() WHERE workspace_id = @workspace_id AND id = @id AND archived_at IS NULL RETURNING *;

-- name: ArchiveOwnedAgents :many
UPDATE agent SET archived_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id AND owner_id = @owner_id AND archived_at IS NULL AND built_in_key='' RETURNING *;
