-- The researcher's own agent for one member of a Team version.
-- name: GetTeamAgent :one
SELECT a.* FROM team_agent t JOIN agent a ON a.workspace_id = t.workspace_id AND a.id = t.agent_id
WHERE t.workspace_id = @workspace_id AND t.pack_id = @pack_id AND t.user_id = @user_id AND t.member = @member;
