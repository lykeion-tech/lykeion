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
