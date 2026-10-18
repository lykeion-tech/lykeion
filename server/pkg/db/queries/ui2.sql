-- name: ListWorkspaceSkills :many
SELECT s.workspace_id, s.id, s.name, s.revision, s.created_by, s.source_kind, s.source_location,
       s.source_runtime_id, s.created_at, s.updated_at, v.description, v.domain, v.manifest
FROM workspace_skill s JOIN workspace_skill_version v
  ON (v.workspace_id, v.skill_id, v.revision) = (s.workspace_id, s.id, s.revision)
WHERE s.workspace_id = @workspace_id AND s.deleted_at IS NULL
ORDER BY s.name, s.id;

-- name: GetWorkspaceSkill :one
SELECT s.*, v.description, v.domain, v.sha256, v.manifest, v.blob_key
FROM workspace_skill s JOIN workspace_skill_version v
  ON (v.workspace_id, v.skill_id, v.revision) = (s.workspace_id, s.id, s.revision)
WHERE s.workspace_id = @workspace_id AND s.id = @id AND s.deleted_at IS NULL;

-- name: LockWorkspaceSkill :one
SELECT * FROM workspace_skill WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: InsertWorkspaceSkill :one
INSERT INTO workspace_skill (workspace_id, id, name, created_by, source_kind, source_location, source_runtime_id)
VALUES (@workspace_id, @id, @name, @created_by, @source_kind, @source_location, @source_runtime_id)
RETURNING *;

-- name: InsertWorkspaceSkillVersion :one
INSERT INTO workspace_skill_version (workspace_id, skill_id, revision, name, description, sha256, manifest, blob_key)
VALUES (@workspace_id, @skill_id, @revision, @name, @description, @sha256, @manifest, @blob_key)
RETURNING *;

-- name: GetWorkspaceSkillVersion :one
SELECT * FROM workspace_skill_version
WHERE workspace_id = @workspace_id AND skill_id = @skill_id AND revision = @revision;

-- name: UpdateWorkspaceSkill :one
UPDATE workspace_skill SET name = @name, revision = revision + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND revision = @expected_revision AND deleted_at IS NULL
RETURNING *;

-- name: DeleteWorkspaceSkill :one
UPDATE workspace_skill SET deleted_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND revision = @expected_revision AND deleted_at IS NULL
RETURNING *;

-- name: PinWorkspaceSkill :exec
INSERT INTO workspace_skill_pin (workspace_id, scope_kind, scope_id, skill_id, revision)
VALUES (@workspace_id, @scope_kind, @scope_id, @skill_id, @revision)
ON CONFLICT (workspace_id, scope_kind, scope_id, skill_id) DO NOTHING;

-- name: GetWorkspaceSkillPin :one
SELECT v.* FROM workspace_skill_pin p JOIN workspace_skill_version v USING (workspace_id, skill_id, revision)
WHERE p.workspace_id = @workspace_id AND p.scope_kind = @scope_kind AND p.scope_id = @scope_id AND p.skill_id = @skill_id;

-- name: ListWorkspaceSkillPins :many
SELECT v.* FROM workspace_skill_pin p JOIN workspace_skill_version v USING (workspace_id, skill_id, revision)
WHERE p.workspace_id = @workspace_id AND p.scope_kind = @scope_kind AND p.scope_id = @scope_id
ORDER BY v.name, v.skill_id;

-- name: ListWorkspaceMCPServers :many
SELECT workspace_id, id, name, transport, revision, created_by, created_at, updated_at
FROM workspace_mcp_server WHERE workspace_id = @workspace_id ORDER BY name, id;

-- name: GetWorkspaceMCPServer :one
SELECT workspace_id, id, name, transport, revision, created_by, created_at, updated_at
FROM workspace_mcp_server WHERE workspace_id = @workspace_id AND id = @id;

-- name: LockWorkspaceMCPServer :one
SELECT * FROM workspace_mcp_server WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: GetWorkspaceMCPServerConfig :one
SELECT config_ciphertext FROM workspace_mcp_server WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertWorkspaceMCPServer :one
INSERT INTO workspace_mcp_server (workspace_id, id, name, transport, config_ciphertext, created_by)
VALUES (@workspace_id, @id, @name, @transport, @config_ciphertext, @created_by)
RETURNING workspace_id, id, name, transport, revision, created_by, created_at, updated_at;

-- name: UpdateWorkspaceMCPServer :one
UPDATE workspace_mcp_server
SET name = @name, transport = @transport, config_ciphertext = @config_ciphertext, revision = revision + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND revision = @expected_revision
RETURNING workspace_id, id, name, transport, revision, created_by, created_at, updated_at;

-- name: DeleteWorkspaceMCPServer :one
DELETE FROM workspace_mcp_server WHERE workspace_id = @workspace_id AND id = @id AND revision = @expected_revision
RETURNING workspace_id, id, name, transport, revision, created_by, created_at, updated_at;

-- name: InsertRuntimeSkillRequest :one
INSERT INTO runtime_skill_request (workspace_id, id, runtime_id, owner_id, daemon_id, operation, keys)
VALUES (@workspace_id, @id, @runtime_id, @owner_id, @daemon_id, @operation, @keys)
RETURNING *;

-- name: GetRuntimeSkillRequest :one
SELECT * FROM runtime_skill_request
WHERE workspace_id = @workspace_id AND id = @id AND runtime_id = @runtime_id AND owner_id = @owner_id
  AND (status IN ('pending', 'running') OR completed_at > now() - interval '5 minutes');

-- name: LockRuntimeSkillRequest :one
SELECT * FROM runtime_skill_request
WHERE workspace_id = @workspace_id AND id = @id AND runtime_id = @runtime_id AND owner_id = @owner_id
FOR UPDATE;

-- name: ListPendingRuntimeSkillRequests :many
SELECT q.* FROM runtime_skill_request q JOIN runtime r ON (r.workspace_id, r.id, r.owner_id, r.daemon_id) = (q.workspace_id, q.runtime_id, q.owner_id, q.daemon_id)
WHERE q.workspace_id = @workspace_id AND q.runtime_id = @runtime_id AND q.owner_id = @owner_id AND q.daemon_id = @daemon_id
  AND q.status = 'pending' AND q.expires_at > now() AND r.status = 'online'
ORDER BY q.created_at, q.id;

-- name: ClaimRuntimeSkillRequest :one
UPDATE runtime_skill_request q
SET status = 'running', acquisition_id = @acquisition_id, acquired_at = now(), expires_at = now() + interval '60 seconds'
FROM runtime r
WHERE q.workspace_id = @workspace_id AND q.id = @id AND q.runtime_id = @runtime_id
  AND q.owner_id = @owner_id AND q.daemon_id = @daemon_id
  AND q.status = 'pending' AND q.expires_at > now()
  AND (r.workspace_id, r.id, r.owner_id, r.daemon_id) = (q.workspace_id, q.runtime_id, q.owner_id, q.daemon_id) AND r.status = 'online'
RETURNING q.*;

-- name: CompleteRuntimeSkillRequest :one
UPDATE runtime_skill_request q
SET status = @status, result = @result, result_blob_key = @result_blob_key, error = @error, completed_at = now()
FROM runtime r
WHERE q.workspace_id = @workspace_id AND q.id = @id AND q.runtime_id = @runtime_id
  AND q.owner_id = @owner_id AND q.daemon_id = @daemon_id AND q.acquisition_id = @acquisition_id
  AND q.status = 'running' AND q.expires_at > now() AND @status::text IN ('completed', 'failed')
  AND (r.workspace_id, r.id, r.owner_id, r.daemon_id) = (q.workspace_id, q.runtime_id, q.owner_id, q.daemon_id)
RETURNING q.*;

-- name: TimeoutRuntimeSkillRequests :many
UPDATE runtime_skill_request SET status = 'timeout', completed_at = now()
WHERE workspace_id = @workspace_id AND runtime_id = @runtime_id AND owner_id = @owner_id
  AND status IN ('pending', 'running') AND expires_at <= now()
RETURNING *;

-- name: UpdateWorkspaceConnectorRevision :one
UPDATE workspace_connector
SET enabled = @enabled, key_ciphertext = @key_ciphertext, licence_confirmed_by = @licence_confirmed_by,
    licence_confirmed_at = @licence_confirmed_at, updated_by = @updated_by, updated_at = now(), revision = revision + 1
WHERE workspace_id = @workspace_id AND name = @name AND revision = @expected_revision
RETURNING *;

-- name: UpdateRuntimeSkillRequestResult :one
UPDATE runtime_skill_request q
SET status = @status, result = @result,
    completed_at = CASE WHEN @status::text IN ('completed','failed') THEN clock_timestamp() ELSE NULL END,
    error = @error
FROM runtime r
WHERE q.workspace_id = @workspace_id AND q.id = @id AND q.runtime_id = @runtime_id
  AND q.owner_id = @owner_id AND q.daemon_id = @daemon_id AND q.acquisition_id = @acquisition_id
  AND q.status = 'running' AND q.expires_at > clock_timestamp()
  AND @status::text IN ('running','completed','failed')
  AND (r.workspace_id,r.id,r.owner_id,r.daemon_id) = (q.workspace_id,q.runtime_id,q.owner_id,q.daemon_id)
RETURNING q.*;

-- name: RuntimeSweepCandidates :many
SELECT runtime_id::uuid AS runtime_id, workspace_id::uuid AS workspace_id
FROM runtime_sweep_candidates(@stale::interval);

-- name: SweepWorkspaceRuntimes :execrows
UPDATE runtime SET status = 'offline'
WHERE workspace_id = @workspace_id AND status = 'online' AND last_seen_at < now() - @stale::interval;
