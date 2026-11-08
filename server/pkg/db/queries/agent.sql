-- name: CreateAgent :one
INSERT INTO agent (workspace_id, name, description, instructions, harness, model, visibility, owner_id, conversation_starters)
VALUES (@workspace_id, @name, @description, @instructions, @harness, @model, @visibility, @owner_id, coalesce(sqlc.narg(conversation_starters)::jsonb, '[]'::jsonb)) RETURNING *;

-- name: ListAgents :many
SELECT a.*, EXISTS (SELECT 1 FROM team_agent t WHERE t.workspace_id = a.workspace_id AND t.agent_id = a.id) AS team_member,
  CASE WHEN a.built_in_key <> '' OR EXISTS (
    SELECT 1 FROM team_agent t JOIN field_pack p ON p.workspace_id = t.workspace_id AND p.id = t.pack_id
    WHERE t.workspace_id = a.workspace_id AND t.agent_id = a.id AND p.source = 'shipped'
  ) THEN 'built_in' ELSE 'custom' END::text AS source
FROM agent a WHERE a.workspace_id = @workspace_id AND a.team_deleted_at IS NULL AND a.deleted_at IS NULL
AND (a.visibility = 'workspace' OR a.owner_id = @user_id OR @is_admin::boolean)
ORDER BY a.created_at, a.id;

-- name: AgentIsTeamCopy :one
SELECT EXISTS (SELECT 1 FROM team_agent WHERE workspace_id = @workspace_id AND agent_id = @agent_id);

-- name: GetAgent :one
SELECT * FROM agent WHERE workspace_id = @workspace_id AND id = @id AND team_deleted_at IS NULL AND deleted_at IS NULL;

-- name: AgentHarnessForHistory :one
SELECT harness FROM agent WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetAgentForUpdate :one
-- Changing an agent waits for the studies being started with it.
SELECT * FROM agent WHERE workspace_id = @workspace_id AND id = @id AND team_deleted_at IS NULL AND deleted_at IS NULL FOR UPDATE;

-- name: GetAgentForShare :one
-- Starting a study with an agent holds off changes to it until the study is in.
SELECT * FROM agent WHERE workspace_id = @workspace_id AND id = @id AND team_deleted_at IS NULL AND deleted_at IS NULL FOR SHARE;

-- name: UpdateAgent :one
UPDATE agent SET name = @name, description = @description, instructions = @instructions, harness = @harness, model = @model, visibility = @visibility, conversation_starters = @conversation_starters, avatar_url = @avatar_url, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND archived_at IS NULL RETURNING *;

-- name: ArchiveAgent :one
UPDATE agent SET archived_at = now(), updated_at = now() WHERE workspace_id = @workspace_id AND id = @id AND archived_at IS NULL RETURNING *;

-- name: DeleteAgent :exec
UPDATE agent SET deleted_at = now(), archived_at = COALESCE(archived_at, now()), updated_at = now(),
  description = '', instructions = '', model = '', avatar_url = '', conversation_starters = '[]'::jsonb
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL;

-- name: AgentHasLiveRuns :one
SELECT EXISTS (SELECT 1 FROM agent_run WHERE workspace_id = @workspace_id AND agent_id = @agent_id AND status IN ('queued', 'dispatched', 'running'));

-- name: AgentUses :many
-- The studies still working with an agent (Blocked ones and mixed Autoresearch
-- studies included) and the Autoresearches that name it, studies first.
SELECT kind::text AS kind, label::text AS label, owner::text AS owner FROM (
  SELECT 0 AS ord, c.number AS n, 'study' AS kind, '#' || c.number AS label, COALESCE(NULLIF(u.name, ''), u.email) AS owner
  FROM card c JOIN "user" u ON u.id = c.owner_id
  WHERE c.workspace_id = @workspace_id AND c.assignee_id = @agent_id AND c.ending = '' AND c.state <> 'done'
  UNION ALL
  SELECT 1, 0, 'autoresearch', r.title, COALESCE(NULLIF(u.name, ''), u.email)
  FROM autoresearch r JOIN "user" u ON u.id = r.owner_id
  WHERE r.workspace_id = @workspace_id AND r.assignee_id = @agent_id
) x ORDER BY ord, n, label;

-- name: ArchiveOwnedAgents :many
UPDATE agent SET archived_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id AND owner_id = @owner_id AND archived_at IS NULL AND built_in_key='' AND laboratory = '' RETURNING *;

-- name: OldestOwner :one
SELECT user_id FROM member WHERE workspace_id = @workspace_id AND role = 'owner' ORDER BY created_at, user_id LIMIT 1;

-- Every person's private copy of one Team member, oldest first.
-- name: TeamMemberCopies :many
SELECT a.* FROM team_agent t JOIN agent a ON a.workspace_id = t.workspace_id AND a.id = t.agent_id
WHERE t.workspace_id = @workspace_id AND t.pack_id = @pack_id AND t.member = @member AND t.user_id IS NOT NULL
ORDER BY a.created_at, a.id;

-- name: MakeLaboratoryAgent :one
UPDATE agent SET name = @name, description = @description, instructions = @instructions, harness = @harness, model = @model,
  visibility = 'workspace', owner_id = @owner_id, laboratory = 'built-in', stage = @stage, avatar_url = '', conversation_starters = '[]'::jsonb,
  archived_at = NULL, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- Merges the private copies of a laboratory member into its shared agent and
-- names the agents whose own customisation rows were dropped.
-- name: LaboratoryMerge :one
SELECT laboratory_merge(@keep::uuid)::uuid[] AS dropped;

-- name: BindAgentRuntime :one
UPDATE agent SET runtime_id = @runtime_id, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND laboratory <> '' RETURNING *;

-- The laboratory agents with no runtime yet, oldest first.
-- name: UnboundLaboratoryAgents :many
SELECT * FROM agent WHERE workspace_id = @workspace_id AND laboratory <> '' AND runtime_id IS NULL
  AND deleted_at IS NULL AND archived_at IS NULL ORDER BY created_at, id FOR UPDATE;

-- The Team member a laboratory agent is, with its Team's manifest.
-- name: LaboratoryMember :one
SELECT t.member, p.manifest FROM team_agent t JOIN field_pack p ON p.workspace_id = t.workspace_id AND p.id = t.pack_id
WHERE t.workspace_id = @workspace_id AND t.agent_id = @agent_id AND t.user_id IS NULL LIMIT 1;

-- A laboratory agent's queued runs follow it to a runtime with another CLI.
-- name: RetargetLaboratoryRuns :exec
UPDATE agent_run SET harness = @harness, requested_model = @requested_model
WHERE workspace_id = @workspace_id AND agent_id = @agent_id AND kind = 'role' AND status = 'queued' AND harness <> @harness;
