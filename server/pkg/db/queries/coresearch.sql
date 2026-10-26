-- name: CreateCoresearchProject :one
SELECT public.coresearch_create_project(@name::text)::uuid AS id;

-- name: ListCoresearchProjects :many
SELECT p.*, m.role FROM coresearch_project p JOIN coresearch_member m ON m.workspace_id=p.workspace_id AND m.project_id=p.id AND m.user_id=app_user()
WHERE p.workspace_id = @workspace_id ORDER BY p.updated_at DESC,p.id;

-- name: GetCoresearchProject :one
SELECT * FROM coresearch_project WHERE workspace_id = @workspace_id AND id = @id;

-- name: LockCoresearchProject :one
SELECT * FROM coresearch_project WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: RenameCoresearchProject :one
UPDATE coresearch_project SET name = @name,updated_at=now() WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: ArchiveCoresearchProject :one
UPDATE coresearch_project SET archived_at=now(),updated_at=now() WHERE workspace_id = @workspace_id AND id = @id RETURNING *;

-- name: TouchCoresearchProject :exec
UPDATE coresearch_project SET updated_at=now() WHERE workspace_id = @workspace_id AND id = @id;

-- name: CoresearchMember :one
SELECT * FROM coresearch_member WHERE workspace_id = @workspace_id AND project_id = @project_id AND user_id = @user_id;

-- name: ListCoresearchMembers :many
SELECT * FROM coresearch_member WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY created_at,user_id;

-- name: CountCoresearchOwners :one
SELECT count(*) FROM coresearch_member WHERE workspace_id = @workspace_id AND project_id = @project_id AND role='owner';

-- name: SetCoresearchMember :one
INSERT INTO coresearch_member(workspace_id,project_id,user_id,role) VALUES(@workspace_id,@project_id,@user_id,@role)
ON CONFLICT (project_id,user_id) DO UPDATE SET role=excluded.role RETURNING *;

-- name: DeleteCoresearchMember :exec
DELETE FROM coresearch_member WHERE workspace_id = @workspace_id AND project_id = @project_id AND user_id = @user_id;

-- name: EnsureCoresearchFile :exec
INSERT INTO coresearch_file(workspace_id,project_id,path) VALUES(@workspace_id,@project_id,@path) ON CONFLICT(project_id,path) DO NOTHING;

-- name: LockCoresearchFile :one
SELECT * FROM coresearch_file WHERE workspace_id = @workspace_id AND project_id = @project_id AND path = @path FOR UPDATE;

-- name: GetCoresearchFile :one
SELECT * FROM coresearch_file WHERE workspace_id = @workspace_id AND project_id = @project_id AND id = @id;

-- name: ListCoresearchFiles :many
SELECT * FROM coresearch_file WHERE workspace_id = @workspace_id AND project_id = @project_id ORDER BY path;

-- name: CoresearchFileHead :one
SELECT * FROM coresearch_file_version WHERE workspace_id = @workspace_id AND file_id = @file_id ORDER BY n DESC LIMIT 1;

-- name: CoresearchFileVersion :one
SELECT * FROM coresearch_file_version WHERE workspace_id = @workspace_id AND file_id = @file_id AND n = @n;

-- name: ListCoresearchFileVersions :many
SELECT * FROM coresearch_file_version WHERE workspace_id = @workspace_id AND file_id = @file_id ORDER BY n DESC;

-- name: CoresearchProjectBytes :one
SELECT coalesce(sum(blobs.size),0)::bigint AS bytes FROM (
SELECT v.sha256,max(v.size)::bigint AS size FROM coresearch_file_version v JOIN coresearch_file f ON f.workspace_id=v.workspace_id AND f.id=v.file_id
WHERE f.workspace_id = @workspace_id AND f.project_id = @project_id AND v.sha256 <> @sha256 GROUP BY v.sha256) blobs;

-- name: InsertCoresearchVersion :one
INSERT INTO coresearch_file_version(workspace_id,file_id,n,sha256,size,blob_key,author_user_id,agent_run_id,run_id,overrode,restored_from)
VALUES(@workspace_id,@file_id,@n,@sha256,@size,@blob_key,sqlc.narg(author_user_id),sqlc.narg(agent_run_id),sqlc.narg(run_id),sqlc.narg(overrode),sqlc.narg(restored_from)) RETURNING *;
