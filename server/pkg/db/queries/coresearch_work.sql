-- name: EnqueueCoresearchCell :one
INSERT INTO coresearch_cell(workspace_id,chat_id,seq,author_user_id,agent_run_id,kernel,source,status)
VALUES( @workspace_id, @chat_id,(SELECT coalesce(max(seq),0)+1 FROM coresearch_cell WHERE chat_id= @chat_id),sqlc.narg(author_user_id),sqlc.narg(agent_run_id), @kernel, @source,'queued') RETURNING *;
-- name: ListCoresearchCells :many
SELECT * FROM coresearch_cell WHERE workspace_id= @workspace_id AND chat_id= @chat_id ORDER BY seq;
-- name: GetCoresearchCell :one
SELECT * FROM coresearch_cell WHERE workspace_id= @workspace_id AND id= @id;
-- name: ClaimCoresearchCell :one
UPDATE coresearch_cell SET status='running',claimed_daemon_id= @daemon_id WHERE id=(SELECT c.id FROM coresearch_cell c WHERE c.workspace_id= @workspace_id AND c.chat_id= @chat_id AND c.status='queued' AND NOT EXISTS(SELECT 1 FROM coresearch_cell x WHERE x.chat_id=c.chat_id AND x.status='running') ORDER BY c.seq LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING *;
-- name: StartCoresearchCell :exec
UPDATE coresearch_cell SET started_at=coalesce(started_at,now()) WHERE workspace_id= @workspace_id AND id= @id AND status='running';
-- name: FinishCoresearchCell :one
UPDATE coresearch_cell SET status= @status,execution_count=sqlc.narg(execution_count),outputs=sqlc.narg(outputs),outputs_blob=sqlc.narg(outputs_blob),finished_at=now() WHERE workspace_id= @workspace_id AND id= @id AND status='running' RETURNING *;
-- name: RequestCoresearchKernel :exec
UPDATE coresearch_chat SET kernel_request= @op,kernel_request_seq=kernel_request_seq+1 WHERE workspace_id= @workspace_id AND id= @id;
-- name: ReportCoresearchKernel :exec
UPDATE coresearch_chat SET kernel_state= @state,kernel_seen_at=now(),kernel_request=CASE WHEN kernel_request_seq= @seq_done THEN NULL ELSE kernel_request END WHERE workspace_id= @workspace_id AND id= @id;
-- name: CoresearchFileByPath :one
SELECT * FROM coresearch_file WHERE workspace_id= @workspace_id AND project_id= @project_id AND path= @path;
-- name: NextCoresearchRunKey :one
SELECT ('R'||(coalesce(max(substring(key from 2)::bigint),0)+1))::text AS key FROM coresearch_run WHERE workspace_id= @workspace_id AND project_id= @project_id;
-- name: EnqueueCoresearchRun :one
INSERT INTO coresearch_run(workspace_id,project_id,key,chat_id,script_file_id,script_n,inputs,seed,expected_env_sha,status,rerun_of,requested_by,agent_run_id)
VALUES( @workspace_id, @project_id, @key, @chat_id, @script_file_id, @script_n, @inputs, @seed,sqlc.narg(expected_env_sha),'queued',sqlc.narg(rerun_of), @requested_by,sqlc.narg(agent_run_id)) RETURNING *;
-- name: ListCoresearchRuns :many
SELECT * FROM coresearch_run WHERE workspace_id= @workspace_id AND project_id= @project_id ORDER BY created_at,id;
-- name: GetCoresearchRunKey :one
SELECT * FROM coresearch_run WHERE workspace_id= @workspace_id AND project_id= @project_id AND key= @key;
-- name: GetCoresearchRun :one
SELECT * FROM coresearch_run WHERE workspace_id= @workspace_id AND id= @id;
-- name: ClaimCoresearchRuns :many
UPDATE coresearch_run SET status='running',claimed_daemon_id= @daemon_id WHERE id IN(SELECT q.id FROM coresearch_run q WHERE q.workspace_id= @workspace_id AND q.chat_id= @chat_id AND q.status='queued' ORDER BY q.created_at LIMIT 8 FOR UPDATE SKIP LOCKED) RETURNING *;
-- name: FinishCoresearchRun :one
UPDATE coresearch_run SET status= @status,env_sha=sqlc.narg(env_sha),facts= @facts,finished_at=now() WHERE workspace_id= @workspace_id AND id= @id AND status='running' RETURNING *;
-- name: CoresearchRunOutputs :many
SELECT f.path,f.id AS file_id,v.n,v.sha256,v.size FROM coresearch_file_version v JOIN coresearch_file f ON f.workspace_id=v.workspace_id AND f.id=v.file_id WHERE v.workspace_id= @workspace_id AND v.run_id= @run_id ORDER BY f.path,v.n;
-- name: EnqueueCoresearchSetup :one
INSERT INTO coresearch_setup(workspace_id,project_id,chat_id,python,conda,status,requested_by) VALUES( @workspace_id, @project_id, @chat_id,sqlc.narg(python), @conda,'queued', @requested_by) RETURNING *;
-- name: ListCoresearchSetups :many
SELECT * FROM coresearch_setup WHERE workspace_id= @workspace_id AND project_id= @project_id ORDER BY created_at,id;
-- name: GetCoresearchSetup :one
SELECT * FROM coresearch_setup WHERE workspace_id= @workspace_id AND id= @id;
-- name: ClaimCoresearchSetups :many
UPDATE coresearch_setup SET status='running',claimed_daemon_id= @daemon_id WHERE id IN(SELECT s.id FROM coresearch_setup s WHERE s.workspace_id= @workspace_id AND s.chat_id= @chat_id AND s.status='queued' AND NOT EXISTS(SELECT 1 FROM coresearch_setup x WHERE x.project_id=s.project_id AND x.status='running') ORDER BY s.created_at LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING *;
-- name: FinishCoresearchSetup :one
UPDATE coresearch_setup SET status= @status,log= @log,finished_at=now() WHERE workspace_id= @workspace_id AND id= @id AND status='running' RETURNING *;
-- name: SetCoresearchPython :exec
UPDATE coresearch_project SET python_version= @python_version WHERE workspace_id= @workspace_id AND id= @id;
-- name: CoresearchClaimChats :many
SELECT c.* FROM coresearch_chat c JOIN coresearch_project p ON p.workspace_id=c.workspace_id AND p.id=c.project_id
WHERE c.workspace_id= @workspace_id AND c.owner_id= @owner_id AND p.archived_at IS NULL AND (c.daemon_id IS NULL OR c.daemon_id= @daemon_id)
AND EXISTS(SELECT 1 FROM coresearch_member m WHERE m.workspace_id=c.workspace_id AND m.project_id=c.project_id AND m.user_id=c.owner_id)
AND EXISTS(SELECT 1 FROM runtime r WHERE r.workspace_id=c.workspace_id AND r.id=ANY( @runtime_ids::uuid[]) AND r.daemon_id= @daemon_id AND r.owner_id=c.owner_id AND r.harness=c.cli)
AND (c.kernel_request IS NOT NULL
OR (EXISTS(SELECT 1 FROM coresearch_cell x WHERE x.workspace_id=c.workspace_id AND x.chat_id=c.id AND x.status='queued') AND NOT EXISTS(SELECT 1 FROM coresearch_cell x WHERE x.workspace_id=c.workspace_id AND x.chat_id=c.id AND x.status='running'))
OR EXISTS(SELECT 1 FROM coresearch_run x WHERE x.workspace_id=c.workspace_id AND x.chat_id=c.id AND x.status='queued')
OR (EXISTS(SELECT 1 FROM coresearch_setup x WHERE x.workspace_id=c.workspace_id AND x.chat_id=c.id AND x.status='queued') AND NOT EXISTS(SELECT 1 FROM coresearch_setup x WHERE x.workspace_id=c.workspace_id AND x.project_id=c.project_id AND x.status='running')))
ORDER BY c.project_id,c.id LIMIT 64 FOR UPDATE OF c SKIP LOCKED;
-- name: CoresearchOwnedDaemon :one
SELECT EXISTS(SELECT 1 FROM runtime WHERE workspace_id= @workspace_id AND daemon_id= @daemon_id AND owner_id= @owner_id)::boolean;
-- name: CoresearchBlobForOwner :one
SELECT v.* FROM coresearch_file_version v JOIN coresearch_file f ON f.workspace_id=v.workspace_id AND f.id=v.file_id WHERE v.workspace_id= @workspace_id AND v.sha256= @sha256 AND EXISTS(SELECT 1 FROM coresearch_chat c WHERE c.workspace_id=f.workspace_id AND c.project_id=f.project_id AND c.owner_id= @owner_id AND c.daemon_id= @daemon_id) LIMIT 1;
-- name: SetCoresearchRevokedReason :exec
UPDATE agent_run SET failure_reason='coresearch_access_revoked' WHERE workspace_id= @workspace_id AND id= @id;
-- name: RevokeCoresearchWork :many
SELECT kind::text,project_id::uuid,chat_id::uuid,work_id::uuid FROM public.coresearch_revoke(@target::uuid,sqlc.narg(project)::uuid);
-- name: CoresearchChatByID :one
SELECT * FROM coresearch_chat WHERE workspace_id= @workspace_id AND id= @id;
