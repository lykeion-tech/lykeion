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

-- name: CreateCoresearchChat :one
INSERT INTO coresearch_chat(workspace_id,project_id,owner_id,mode,cli) VALUES(@workspace_id,@project_id,@owner_id,'experiment',@cli) RETURNING *;

-- name: GetCoresearchChat :one
SELECT * FROM coresearch_chat WHERE workspace_id= @workspace_id AND project_id= @project_id AND id= @id;

-- name: LockCoresearchChat :one
SELECT * FROM coresearch_chat WHERE workspace_id= @workspace_id AND id= @id FOR UPDATE;

-- name: ListCoresearchChats :many
SELECT c.*, coalesce((SELECT coalesce(r.custom_name,r.device_name) FROM runtime r WHERE r.workspace_id=c.workspace_id AND r.daemon_id=c.daemon_id AND r.owner_id=c.owner_id AND r.harness=c.cli LIMIT 1), '')::text AS machine_name
FROM coresearch_chat c WHERE c.workspace_id= @workspace_id AND c.project_id= @project_id ORDER BY c.created_at,c.id;

-- name: BindCoresearchChat :exec
UPDATE coresearch_chat SET daemon_id= @daemon_id WHERE workspace_id= @workspace_id AND id= @id AND daemon_id IS NULL;

-- name: ReleaseCoresearchChat :exec
UPDATE coresearch_chat SET daemon_id=NULL,kernel_state='stopped',kernel_seen_at=NULL WHERE workspace_id= @workspace_id AND id= @id;

-- name: CoresearchChatTurnBusy :one
SELECT EXISTS(SELECT 1 FROM agent_run WHERE workspace_id= @workspace_id AND coresearch_chat_id= @chat_id AND status IN ('queued','dispatched','running'))::boolean;

-- name: CoresearchChatBusy :one
SELECT (EXISTS(SELECT 1 FROM agent_run a WHERE a.workspace_id= @workspace_id AND a.coresearch_chat_id= @chat_id::uuid AND a.status IN ('queued','dispatched','running'))
OR EXISTS(SELECT 1 FROM coresearch_cell c WHERE c.workspace_id= @workspace_id AND c.chat_id= @chat_id::uuid AND c.status IN ('queued','running'))
OR EXISTS(SELECT 1 FROM coresearch_run r WHERE r.workspace_id= @workspace_id AND r.chat_id= @chat_id::uuid AND r.status IN ('queued','running'))
OR EXISTS(SELECT 1 FROM coresearch_setup e WHERE e.workspace_id= @workspace_id AND e.chat_id= @chat_id::uuid AND e.status IN ('queued','running')))::boolean AS busy;

-- name: InsertCoresearchMessage :one
INSERT INTO coresearch_message(workspace_id,chat_id,kind,author_user_id,agent_run_id,body) VALUES(@workspace_id,@chat_id,@kind,sqlc.narg(author_user_id),sqlc.narg(agent_run_id),@body) RETURNING *;

-- name: AttachCoresearchTurn :exec
UPDATE coresearch_message SET agent_run_id= @agent_run_id WHERE workspace_id= @workspace_id AND id= @id;

-- name: ListCoresearchMessages :many
SELECT m.*,r.status,r.failure_reason FROM coresearch_message m LEFT JOIN agent_run r ON r.workspace_id=m.workspace_id AND r.id=m.agent_run_id WHERE m.workspace_id= @workspace_id AND m.chat_id= @chat_id ORDER BY m.created_at,m.id;

-- name: CoresearchHistory :many
SELECT * FROM (SELECT * FROM coresearch_message WHERE workspace_id= @workspace_id AND chat_id= @chat_id ORDER BY created_at DESC,id DESC LIMIT 20) history ORDER BY created_at,id;

-- name: EnqueueCoresearchAgentRun :one
INSERT INTO agent_run(workspace_id,kind,requester_id,responsible_user_id,board_column,round,role,harness,prompt,skill,max_attempts,requested_model,continuity_scope,coresearch_chat_id)
VALUES(@workspace_id,'coresearch',@owner_id::uuid,@owner_id::uuid,'',0,'coresearch',@harness,@prompt,'',2,'',@continuity_scope,@coresearch_chat_id) RETURNING *;

-- name: SettleCoresearch :many
SELECT project_id::uuid,chat_id::uuid,message_id::uuid FROM public.coresearch_settle(@run_id::uuid);
