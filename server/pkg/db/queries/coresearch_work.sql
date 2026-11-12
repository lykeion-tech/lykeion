-- name: EnqueueCoresearchCell :one
INSERT INTO coresearch_cell(workspace_id,chat_id,seq,author_user_id,agent_run_id,kernel,source,status)
VALUES( @workspace_id, @chat_id,(SELECT coalesce(max(seq),0)+1 FROM coresearch_cell WHERE chat_id= @chat_id),sqlc.narg(author_user_id),sqlc.narg(agent_run_id), @kernel, @source,'queued') RETURNING *;
-- name: ListCoresearchCells :many
SELECT * FROM coresearch_cell WHERE workspace_id= @workspace_id AND chat_id= @chat_id ORDER BY seq;
-- name: GetCoresearchCell :one
SELECT * FROM coresearch_cell WHERE workspace_id= @workspace_id AND id= @id;
-- name: ClaimCoresearchCell :one
UPDATE coresearch_cell SET status='running',claimed_daemon_id= @daemon_id,claim_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id=(SELECT c.id FROM coresearch_cell c WHERE c.workspace_id= @workspace_id AND c.chat_id= @chat_id AND c.status='queued' AND NOT EXISTS(SELECT 1 FROM coresearch_cell x WHERE x.chat_id=c.chat_id AND x.status='running') ORDER BY c.seq LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING *;
-- name: StartCoresearchCell :exec
UPDATE coresearch_cell SET started_at=coalesce(started_at,now()) WHERE workspace_id= @workspace_id AND id= @id AND status='running';
-- name: FinishCoresearchCell :one
UPDATE coresearch_cell SET status= @status,execution_count=sqlc.narg(execution_count),outputs=sqlc.narg(outputs),outputs_blob=sqlc.narg(outputs_blob),finished_at=now(),receipt_sha= @receipt_sha WHERE workspace_id= @workspace_id AND id= @id AND status='running' RETURNING *;
-- name: RequestCoresearchKernel :exec
UPDATE coresearch_chat SET kernel_request= @op,kernel_request_seq=kernel_request_seq+1 WHERE workspace_id= @workspace_id AND id= @id;
-- name: ReportCoresearchKernel :exec
UPDATE coresearch_chat SET kernel_state= @state,kernel_name=CASE WHEN @state='stopped' THEN '' ELSE coalesce(sqlc.narg(kernel_name)::text,kernel_name) END,kernel_seen_at=now(),kernel_request=CASE WHEN kernel_request_seq= @seq_done THEN NULL ELSE kernel_request END WHERE workspace_id= @workspace_id AND id= @id;
-- name: CoresearchFileByPath :one
SELECT * FROM coresearch_file WHERE workspace_id= @workspace_id AND project_id= @project_id AND path= @path AND deleted_at IS NULL;
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
WITH candidates AS MATERIALIZED (SELECT q.id FROM coresearch_run q WHERE q.workspace_id= @workspace_id AND q.chat_id= @chat_id AND q.status='queued' ORDER BY q.created_at,q.id LIMIT 8 FOR UPDATE SKIP LOCKED) UPDATE coresearch_run SET status='running',claimed_daemon_id= @daemon_id,claim_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id IN(SELECT id FROM candidates) RETURNING *;
-- name: FinishCoresearchRun :one
UPDATE coresearch_run SET status= @status,env_sha=sqlc.narg(env_sha),facts= @facts,finished_at=now(),receipt_sha= @receipt_sha WHERE workspace_id= @workspace_id AND id= @id AND status='running' RETURNING *;
-- name: CoresearchRunOutputs :many
SELECT f.path,f.id AS file_id,v.n,v.sha256,v.size FROM coresearch_file_version v JOIN coresearch_file f ON f.workspace_id=v.workspace_id AND f.id=v.file_id WHERE v.workspace_id= @workspace_id AND v.run_id= @run_id ORDER BY f.path,v.n;
-- name: EnqueueCoresearchSetup :one
INSERT INTO coresearch_setup(workspace_id,project_id,chat_id,python,conda,status,requested_by) VALUES( @workspace_id, @project_id, @chat_id,sqlc.narg(python), @conda,'queued', @requested_by) RETURNING *;
-- name: ListCoresearchSetups :many
SELECT * FROM coresearch_setup WHERE workspace_id= @workspace_id AND project_id= @project_id ORDER BY created_at,id;
-- name: GetCoresearchSetup :one
SELECT * FROM coresearch_setup WHERE workspace_id= @workspace_id AND id= @id;
-- name: ClaimCoresearchSetups :many
WITH candidates AS MATERIALIZED (SELECT s.id FROM coresearch_setup s WHERE s.workspace_id= @workspace_id AND s.chat_id= @chat_id AND s.status='queued' AND NOT EXISTS(SELECT 1 FROM coresearch_setup x WHERE x.project_id=s.project_id AND x.status='running') ORDER BY s.created_at,s.id LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE coresearch_setup SET status='running',claimed_daemon_id= @daemon_id,claim_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id IN(SELECT id FROM candidates) RETURNING *;
-- name: FinishCoresearchSetup :one
UPDATE coresearch_setup SET status= @status,log= @log,finished_at=now(),receipt_sha= @receipt_sha WHERE workspace_id= @workspace_id AND id= @id AND status='running' RETURNING *;
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
SELECT kind::text,project_id::uuid,chat_id::uuid,work_id::uuid FROM public.coresearch_revoke( @target::uuid,sqlc.narg(project)::uuid);
-- name: CoresearchChatByID :one
SELECT * FROM coresearch_chat WHERE workspace_id= @workspace_id AND id= @id;

-- name: HeartbeatCoresearchCell :execrows
UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE workspace_id= @workspace_id AND id= @id AND claim_token= @claim_token AND status='running' AND lease_expires_at>clock_timestamp();

-- name: HeartbeatCoresearchRun :execrows
UPDATE coresearch_run SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE workspace_id= @workspace_id AND id= @id AND claim_token= @claim_token AND status='running' AND lease_expires_at>clock_timestamp();

-- name: HeartbeatCoresearchSetup :execrows
UPDATE coresearch_setup SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE workspace_id= @workspace_id AND id= @id AND claim_token= @claim_token AND status='running' AND lease_expires_at>clock_timestamp();

-- name: CoresearchExpiredWorkspaces :many
SELECT public.coresearch_expired_workspaces()::uuid AS workspace_id;
-- name: ExpireCoresearchWork :many
SELECT kind::text,project_id::uuid,chat_id::uuid,work_id::uuid FROM public.coresearch_expire();

-- name: EnqueueCoresearchBuild :one
INSERT INTO coresearch_build(workspace_id,project_id,seq,requested_by,status,sources)
VALUES( @workspace_id, @project_id,(SELECT coalesce(max(seq),0)+1 FROM coresearch_build WHERE project_id= @project_id), @requested_by,'queued', @sources) RETURNING *;
-- name: QueuedCoresearchBuild :one
SELECT * FROM coresearch_build WHERE workspace_id= @workspace_id AND project_id= @project_id AND requested_by= @requested_by AND status='queued' FOR UPDATE;
-- name: SupersedeCoresearchBuild :one
UPDATE coresearch_build SET status='aborted',failure_reason='superseded',finished_at=now() WHERE workspace_id= @workspace_id AND id= @id AND status='queued' RETURNING *;
-- name: GetCoresearchBuild :one
SELECT * FROM coresearch_build WHERE workspace_id= @workspace_id AND id= @id;
-- name: ListCoresearchBuilds :many
SELECT * FROM coresearch_build WHERE workspace_id= @workspace_id AND project_id= @project_id ORDER BY seq DESC LIMIT @row_limit;
-- name: ClaimCoresearchBuilds :many
WITH candidates AS MATERIALIZED (
 SELECT b.id FROM coresearch_build b JOIN coresearch_project p ON p.workspace_id=b.workspace_id AND p.id=b.project_id
 WHERE b.workspace_id= @workspace_id AND b.requested_by= @owner_id AND b.status='queued' AND p.archived_at IS NULL
 AND EXISTS(SELECT 1 FROM coresearch_member m WHERE m.workspace_id=b.workspace_id AND m.project_id=b.project_id AND m.user_id=b.requested_by)
 AND EXISTS(SELECT 1 FROM runtime r WHERE r.workspace_id=b.workspace_id AND r.id=ANY( @runtime_ids::uuid[]) AND r.daemon_id= @daemon_id AND r.owner_id=b.requested_by)
 AND NOT EXISTS(SELECT 1 FROM coresearch_build x WHERE x.project_id=b.project_id AND x.requested_by=b.requested_by AND x.status='running')
 ORDER BY b.created_at,b.id LIMIT 8 FOR UPDATE OF b SKIP LOCKED)
UPDATE coresearch_build SET status='running',daemon_id= @daemon_id,claimed_daemon_id= @daemon_id,claim_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id IN(SELECT id FROM candidates) RETURNING *;
-- name: StartCoresearchBuild :exec
UPDATE coresearch_build SET started_at=coalesce(started_at,now()) WHERE workspace_id= @workspace_id AND id= @id AND status='running';
-- name: HeartbeatCoresearchBuild :execrows
UPDATE coresearch_build SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE workspace_id= @workspace_id AND id= @id AND claim_token= @claim_token AND status='running' AND lease_expires_at>clock_timestamp();
-- name: FinishCoresearchBuild :one
UPDATE coresearch_build SET status= @status,pdf_blob=sqlc.narg(pdf_blob),pdf_size= @pdf_size,log_size= @log_size,synctex_size= @synctex_size,log_blob=sqlc.narg(log_blob),synctex_blob=sqlc.narg(synctex_blob),pages=sqlc.narg(pages),failure_reason=sqlc.narg(failure_reason),receipt_sha= @receipt_sha,finished_at=now() WHERE workspace_id= @workspace_id AND id= @id AND status='running' RETURNING *;
-- name: InsertBuildVersion :one
INSERT INTO coresearch_version(workspace_id,project_id,seq,title,by_user_id,build_id,files)
VALUES( @workspace_id, @project_id,(SELECT coalesce(max(seq),0)+1 FROM coresearch_version WHERE project_id= @project_id), @title, @by_user_id, @build_id, @files) RETURNING *;
-- name: CoresearchBuildBlob :one
SELECT v.* FROM coresearch_file_version v WHERE v.workspace_id= @workspace_id AND v.sha256= @sha256 AND EXISTS(
 SELECT 1 FROM coresearch_build b JOIN coresearch_member m ON m.workspace_id=b.workspace_id AND m.project_id=b.project_id AND m.user_id=b.requested_by
 WHERE b.workspace_id=v.workspace_id AND b.requested_by= @owner_id AND b.claimed_daemon_id= @daemon_id AND b.status='running' AND b.lease_expires_at>clock_timestamp()
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(b.sources) x WHERE (x->>'file_id')::uuid=v.file_id AND (x->>'n')::int=v.n)) LIMIT 1;
-- name: CoresearchRunBuild :one
SELECT coresearch_build_id FROM agent_run WHERE workspace_id= @workspace_id AND id= @id;
-- name: SetCoresearchRunBuild :exec
UPDATE agent_run SET coresearch_build_id= @build_id WHERE workspace_id= @workspace_id AND id= @id AND coresearch_build_id IS NULL;
-- name: CoresearchSettlementBuildChat :one
SELECT c.* FROM coresearch_chat c JOIN agent_run r ON r.workspace_id=c.workspace_id AND r.coresearch_chat_id=c.id WHERE r.workspace_id= @workspace_id AND r.id= @id AND c.mode='cowrite' AND r.status IN ('completed','failed','cancelled') AND EXISTS(SELECT 1 FROM coresearch_doc_step d WHERE d.workspace_id=r.workspace_id AND d.agent_run_id=r.id);
-- name: GetCoresearchBuildSeq :one
SELECT * FROM coresearch_build WHERE workspace_id= @workspace_id AND project_id= @project_id AND seq= @seq;

-- name: ListOwnCoresearchKernels :many
SELECT c.id AS chat_id,c.kernel_name AS kernel,
 c.kernel_state AS state,r.id AS runtime_id,coalesce(r.custom_name,r.device_name)::text AS device_name,c.kernel_metrics AS samples
FROM coresearch_chat c JOIN runtime r ON r.workspace_id=c.workspace_id AND r.daemon_id=c.daemon_id AND r.harness=c.cli AND r.owner_id=c.owner_id
WHERE c.workspace_id= @workspace_id AND c.project_id= @project_id AND c.owner_id= @owner_id AND c.kernel_name<>'' ORDER BY c.created_at;

-- name: ListOwnComputeMachines :many
SELECT id AS runtime_id,coalesce(custom_name,device_name)::text AS device_name,metrics FROM runtime WHERE workspace_id= @workspace_id AND owner_id= @owner_id ORDER BY created_at;

-- name: AppendCoresearchKernelSample :exec
UPDATE coresearch_chat SET kernel_seen_at=now(),kernel_name=coalesce(sqlc.narg(kernel_name)::text,kernel_name),kernel_metrics=(SELECT coalesce(jsonb_agg(value ORDER BY ord),'[]'::jsonb) FROM jsonb_array_elements(kernel_metrics||jsonb_build_array(@sample::jsonb)) WITH ORDINALITY a(value,ord) WHERE ord>jsonb_array_length(kernel_metrics)+1-60)
WHERE workspace_id= @workspace_id AND id= @id;

-- name: CoresearchOwnMachineOnline :one
SELECT EXISTS(SELECT 1 FROM runtime WHERE workspace_id= @workspace_id AND owner_id= @owner_id AND status='online' AND last_seen_at>now()-interval '60 seconds')::boolean;
