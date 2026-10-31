-- name: InsertTeamAgent :exec
INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id)
VALUES (@workspace_id, @pack_id, @user_id, @member, @agent_id) ON CONFLICT DO NOTHING;

-- name: TeamAgentsFor :many
SELECT * FROM team_agent WHERE workspace_id = @workspace_id AND pack_id = @pack_id AND user_id = @user_id ORDER BY member;

-- name: TeamAgentIDs :many
SELECT DISTINCT agent_id FROM team_agent WHERE workspace_id = @workspace_id AND pack_id = @pack_id ORDER BY agent_id;

-- name: TeamHasActiveUses :one
SELECT EXISTS (SELECT 1 FROM card c LEFT JOIN card_team t ON t.workspace_id = c.workspace_id AND t.card_id = c.id
  WHERE c.workspace_id = @workspace_id AND (t.pack_id = @pack_id OR c.pack_id = @pack_id) AND c.ending = '' AND c.state <> 'done')
  OR EXISTS (SELECT 1 FROM autoresearch_team WHERE workspace_id = @workspace_id AND pack_id = @pack_id);

-- name: TeamAgentHasLiveRuns :one
SELECT EXISTS (SELECT 1 FROM agent_run WHERE workspace_id = @workspace_id AND agent_id = @agent_id AND status IN ('queued', 'dispatched', 'running'));

-- name: AgentBelongsToOtherTeam :one
SELECT EXISTS (SELECT 1 FROM team_agent WHERE workspace_id = @workspace_id AND agent_id = @agent_id AND pack_id <> @pack_id);

-- name: DeleteTeamAgent :exec
UPDATE agent SET team_deleted_at = now(), archived_at = COALESCE(archived_at, now()), updated_at = now(),
  description = '', instructions = '', model = '', avatar_url = '', conversation_starters = '[]'::jsonb
WHERE workspace_id = @workspace_id AND id = @agent_id AND team_deleted_at IS NULL;

-- name: DeleteTeam :exec
UPDATE field_pack SET deleted_at = now() WHERE workspace_id = @workspace_id AND id = @pack_id AND deleted_at IS NULL;

-- Whether a live agent in the workspace holds the name, matched the way the live-name index does.
-- name: AgentLiveNameTaken :one
SELECT EXISTS (SELECT 1 FROM agent WHERE workspace_id = @workspace_id AND archived_at IS NULL
  AND lower(name) = lower(@name::text)) AS taken;

-- The store: shipped Teams and the user's own uploads.
-- name: StoreTeams :many
SELECT DISTINCT ON (p.name) p.*, (p.source = 'shipped' OR EXISTS (SELECT 1 FROM team_agent t
  WHERE t.workspace_id = p.workspace_id AND t.pack_id = p.id AND t.user_id = @user_id))::bool AS added
FROM field_pack p
WHERE p.workspace_id = @workspace_id AND p.deleted_at IS NULL AND (p.source = 'shipped' OR p.uploaded_by = @user_id)
  AND (@domain::text = '' OR p.domain = @domain) AND (@stage::text = '' OR p.stage = @stage)
ORDER BY p.name, p.created_at DESC;
