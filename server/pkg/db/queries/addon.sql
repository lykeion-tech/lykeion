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

-- The agent runs a gate judged: every run of its round, and every run whose
-- records are still live in the column (earlier rounds' work it read).
-- name: JudgedAgentRuns :many
SELECT t.id, t.role, t.addon_hash, t.profile_mode, t.policy_digest FROM agent_run t
WHERE t.workspace_id = @workspace_id AND t.card_id = @card_id::uuid AND t.board_column = @board_column
  AND (t.round = @round OR EXISTS (
    SELECT 1 FROM record r WHERE r.agent_run_id = t.id AND r.superseded_at IS NULL))
ORDER BY t.created_at;
