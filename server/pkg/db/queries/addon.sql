-- name: GetStageAddon :one
SELECT * FROM stage_addon WHERE workspace_id = @workspace_id AND board_column = @board_column;

-- name: ListStageAddons :many
SELECT * FROM stage_addon WHERE workspace_id = @workspace_id ORDER BY board_column;

-- name: UpsertStageAddon :one
INSERT INTO stage_addon (workspace_id, board_column, skill_md, mcp, updated_by, updated_at)
VALUES (@workspace_id, @board_column, @skill_md, @mcp, @updated_by, now())
ON CONFLICT (workspace_id, board_column) DO UPDATE
  SET skill_md = EXCLUDED.skill_md, mcp = EXCLUDED.mcp, updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: SetAgentRunAddon :exec
UPDATE agent_run SET addon_hash = @addon_hash, addon_mcp = @addon_mcp WHERE id = @id AND workspace_id = @workspace_id;
