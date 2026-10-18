-- name: CreateResearch :one
INSERT INTO research (workspace_id, name, description, created_by) VALUES (@workspace_id, @name, @description, @created_by) RETURNING *;

-- name: ListResearches :many
SELECT * FROM research WHERE workspace_id = @workspace_id ORDER BY created_at, id;

-- name: GetResearch :one
SELECT * FROM research WHERE workspace_id = @workspace_id AND id = @id;

-- name: UpdateResearch :one
UPDATE research SET name = @name, description = @description, updated_at = now() WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: ResearchStudies :many
SELECT * FROM card WHERE workspace_id = @workspace_id AND research_id = @research_id ORDER BY number;

-- name: UpdateCardProperties :one
UPDATE card SET research_id = sqlc.narg(research_id), assignee_type = sqlc.narg(assignee_type), assignee_id = sqlc.narg(assignee_id), version = version + 1
WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: ClearDepartedMemberAssignees :many
UPDATE card SET assignee_type = NULL, assignee_id = NULL, version = version + 1
WHERE workspace_id = @workspace_id AND assignee_type = 'member' AND assignee_id = @user_id RETURNING *;

-- An agent assigned to a Backlog study starts it on the first column of its
-- scope; the column check keeps a started study from being restarted.
-- name: StartBacklogCard :one
UPDATE card SET board_column = scope_from, round = 1, version = version + 1
WHERE workspace_id = @workspace_id AND id = @id AND board_column = 'backlog' RETURNING *;
