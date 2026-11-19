-- name: GetCoresearchDoc :one
SELECT * FROM coresearch_doc WHERE workspace_id= @workspace_id AND file_id= @file_id;
-- name: LockCoresearchDoc :one
SELECT * FROM coresearch_doc WHERE workspace_id= @workspace_id AND file_id= @file_id FOR UPDATE;
-- name: InsertCoresearchDoc :exec
INSERT INTO coresearch_doc(workspace_id,file_id,initial_text,checkpoint_text) VALUES(@workspace_id,@file_id,@initial_text,@initial_text) ON CONFLICT(file_id) DO NOTHING;
-- name: AdvanceCoresearchDoc :exec
UPDATE coresearch_doc SET version= @version,updated_at=now() WHERE workspace_id= @workspace_id AND file_id= @file_id;
-- name: CheckpointCoresearchDoc :exec
UPDATE coresearch_doc SET checkpoint_text= @checkpoint_text,checkpoint_version= @checkpoint_version WHERE workspace_id= @workspace_id AND file_id= @file_id;
-- name: InsertCoresearchDocStep :exec
INSERT INTO coresearch_doc_step(workspace_id,file_id,version,author_user_id,agent_run_id,client_id,changes) VALUES(@workspace_id,@file_id,@version,sqlc.narg(author_user_id),sqlc.narg(agent_run_id),@client_id,@changes);
-- name: ListCoresearchDocSteps :many
SELECT * FROM coresearch_doc_step WHERE workspace_id= @workspace_id AND file_id= @file_id AND version> @after_version AND version<= @through_version ORDER BY version LIMIT 500;
-- name: ListCoresearchPendingRanges :many
SELECT * FROM coresearch_change WHERE workspace_id= @workspace_id AND file_id= @file_id AND status IN ('pending','stale') ORDER BY id FOR UPDATE;
-- name: MapCoresearchChange :exec
UPDATE coresearch_change SET from_pos= @from_pos,to_pos= @to_pos,status= @status WHERE workspace_id= @workspace_id AND id= @id;
-- name: ListCoresearchCommentRanges :many
SELECT * FROM coresearch_comment WHERE workspace_id= @workspace_id AND file_id= @file_id ORDER BY id FOR UPDATE;
-- name: MapCoresearchComment :exec
UPDATE coresearch_comment SET from_pos= @from_pos,to_pos= @to_pos WHERE workspace_id= @workspace_id AND id= @id;
-- name: CoresearchPresenceMembers :many
SELECT m.user_id,u.name FROM coresearch_member m JOIN "user" u ON u.id=m.user_id WHERE m.workspace_id= @workspace_id AND m.project_id= @project_id;
-- name: InsertCoresearchProposal :one
INSERT INTO coresearch_proposal(workspace_id,project_id,chat_id,agent_run_id,restore_of,created_by) VALUES(@workspace_id,@project_id,sqlc.narg(chat_id),sqlc.narg(agent_run_id),sqlc.narg(restore_of),sqlc.narg(created_by)) RETURNING *;
-- name: InsertCoresearchChange :one
INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES(@workspace_id,@proposal_id,@file_id,@from_pos,@to_pos,@removed_text,'pending') RETURNING *;
-- name: GetCoresearchChange :one
SELECT c.* FROM coresearch_change c JOIN coresearch_file f ON f.workspace_id=c.workspace_id AND f.id=c.file_id WHERE c.workspace_id= @workspace_id AND c.id= @id AND f.project_id= @project_id FOR UPDATE OF c;
-- name: DecideCoresearchChange :exec
UPDATE coresearch_change SET status= @status,decided_by= @decided_by,decided_at=now() WHERE workspace_id= @workspace_id AND id= @id;
-- name: ListCoresearchChanges :many
SELECT * FROM coresearch_change WHERE workspace_id= @workspace_id AND file_id= @file_id AND (sqlc.arg(status)::text='' OR status=sqlc.arg(status)::text) ORDER BY created_at,id;
-- name: ListCoresearchVersions :many
WITH manuscripts AS (
    SELECT v.id, main.ref, lag(main.ref) OVER (ORDER BY v.seq) AS previous
    FROM coresearch_version v
    LEFT JOIN LATERAL (
        SELECT ref FROM jsonb_array_elements(v.files) AS source(ref) WHERE ref->>'path' = 'main.tex'
    ) main ON true
    WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id
)
SELECT v.*, coalesce(nullif(btrim(compiler.name), ''), '')::text AS compiled_by_name,
       ARRAY(
           SELECT CASE WHEN authors.author_user_id IS NULL THEN 'AI agent'
                       ELSE coalesce(nullif(btrim(editor.name), ''), 'Unnamed collaborator') END AS name
           FROM (
               SELECT step.author_user_id
               FROM coresearch_doc_step step
               WHERE step.workspace_id = v.workspace_id AND step.file_id = (main.ref->>'file_id')::uuid
                 AND step.version <= (main.ref->>'doc_version')::bigint
                 AND step.version > CASE WHEN main.ref->>'file_id' = main.previous->>'file_id'
                     THEN coalesce((main.previous->>'doc_version')::bigint, 0) ELSE 0 END
               UNION
               SELECT file_version.author_user_id
               FROM coresearch_file_version file_version
               WHERE main.ref->>'doc_version' IS NULL
                 AND file_version.workspace_id = v.workspace_id
                 AND file_version.file_id = (main.ref->>'file_id')::uuid
                 AND file_version.n = (main.ref->>'n')::int
                 AND (file_version.author_user_id IS NOT NULL OR file_version.agent_run_id IS NOT NULL)
           ) authors
           LEFT JOIN "user" editor ON editor.id = authors.author_user_id
           WHERE main.ref->>'sha256' IS DISTINCT FROM main.previous->>'sha256'
           ORDER BY name, authors.author_user_id
       )::text[] AS contributor_names
FROM coresearch_version v
JOIN manuscripts main ON main.id = v.id
LEFT JOIN "user" compiler ON compiler.id = v.by_user_id
WHERE v.workspace_id = @workspace_id AND v.project_id = @project_id
ORDER BY v.seq DESC;
-- name: GetCoresearchVersion :one
SELECT * FROM coresearch_version WHERE workspace_id= @workspace_id AND project_id= @project_id AND seq= @seq;
-- name: ListCoresearchRetainedFiles :many
SELECT * FROM coresearch_file WHERE workspace_id= @workspace_id AND project_id= @project_id ORDER BY path;
-- name: GetCoresearchRetainedFile :one
SELECT * FROM coresearch_file WHERE workspace_id= @workspace_id AND project_id= @project_id AND id= @id;
-- name: SetCoresearchFileDeleted :exec
UPDATE coresearch_file SET deleted_at=CASE WHEN @deleted::boolean THEN now() ELSE NULL END WHERE workspace_id= @workspace_id AND project_id= @project_id AND id= @id;
-- name: SetCoresearchChangeFileAction :exec
UPDATE coresearch_change SET file_action= @file_action WHERE workspace_id= @workspace_id AND id= @id;
-- name: ListCoresearchComments :many
SELECT * FROM coresearch_comment WHERE workspace_id= @workspace_id AND file_id= @file_id ORDER BY created_at,id;
-- name: InsertCoresearchComment :one
INSERT INTO coresearch_comment(workspace_id,file_id,from_pos,to_pos,quote,line,author_user_id,body,mentions) VALUES(@workspace_id,@file_id,@from_pos,@to_pos,@quote,@line,@author_user_id,@body,@mentions) RETURNING *;
-- name: GetCoresearchComment :one
SELECT c.* FROM coresearch_comment c JOIN coresearch_file f ON f.workspace_id=c.workspace_id AND f.id=c.file_id WHERE c.workspace_id= @workspace_id AND c.id= @id AND f.project_id= @project_id AND f.deleted_at IS NULL FOR UPDATE OF c;
-- name: ResolveCoresearchComment :one
UPDATE coresearch_comment SET resolved_by=CASE WHEN @resolved::boolean THEN COALESCE(resolved_by,@resolved_by::uuid) ELSE NULL END,resolved_at=CASE WHEN @resolved::boolean THEN COALESCE(resolved_at,now()) ELSE NULL END WHERE workspace_id= @workspace_id AND id= @id RETURNING *;
-- name: DeleteCoresearchComment :exec
DELETE FROM coresearch_comment WHERE workspace_id= @workspace_id AND id= @id AND author_user_id= @author_user_id;

-- name: ListCoresearchDocumentFiles :many
SELECT d.file_id FROM coresearch_doc d
JOIN coresearch_file f ON f.workspace_id=d.workspace_id AND f.id=d.file_id
WHERE f.workspace_id= @workspace_id AND f.project_id= @project_id AND f.deleted_at IS NULL;
