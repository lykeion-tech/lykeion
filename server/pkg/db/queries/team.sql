-- name: InsertTeamAgent :exec
INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id)
VALUES (@workspace_id, @pack_id, @user_id, @member, @agent_id) ON CONFLICT DO NOTHING;

-- name: TeamAgentsFor :many
SELECT * FROM team_agent WHERE workspace_id = @workspace_id AND pack_id = @pack_id AND user_id = @user_id ORDER BY member;

-- Whether a live agent in the workspace holds the name, matched the way the live-name index does.
-- name: AgentLiveNameTaken :one
SELECT EXISTS (SELECT 1 FROM agent WHERE workspace_id = @workspace_id AND archived_at IS NULL
  AND lower(name) = lower(@name::text)) AS taken;

-- The store: shipped Teams and the user's own uploads, newest version per name.
-- name: StoreTeams :many
SELECT DISTINCT ON (p.name) p.*, EXISTS (SELECT 1 FROM team_agent t
  WHERE t.workspace_id = p.workspace_id AND t.pack_id = p.id AND t.user_id = @user_id)::bool AS added
FROM field_pack p
WHERE p.workspace_id = @workspace_id AND (p.source = 'shipped' OR p.uploaded_by = @user_id)
  AND (@domain::text = '' OR p.domain = @domain) AND (@stage::text = '' OR p.stage = @stage)
ORDER BY p.name, p.created_at DESC;
