-- name: LockAristotleOwner :exec
SELECT pg_advisory_xact_lock(hashtextextended(app_workspace()::text || app_user()::text, 0));

-- name: EnsureAristotle :one
INSERT INTO agent(workspace_id,owner_id,name,description,instructions,harness,model,visibility,built_in_key)
VALUES(app_workspace(),app_user(),'Aristotle','Your personal research partner.','Help the researcher ground questions in evidence, design reproducible experiments, and review findings. Ask clarifying questions, distinguish evidence from uncertainty, and do not start background work.','claude','','private','aristotle')
ON CONFLICT(workspace_id,owner_id,built_in_key) WHERE built_in_key<>'' DO UPDATE SET built_in_key=excluded.built_in_key RETURNING *;

-- name: GetWelcomeConversation :one
SELECT * FROM coresearch_conversation WHERE workspace_id=app_workspace() AND owner_id=app_user() AND is_welcome;

-- name: MarkConversationProject :exec
UPDATE coresearch_project SET conversation_owner_id=app_user() WHERE workspace_id=app_workspace() AND id = @id;

-- name: CreateWelcomeConversation :one
INSERT INTO coresearch_conversation(workspace_id,owner_id,agent_id,project_id,chat_id,title,is_welcome)
VALUES(app_workspace(),app_user(),@agent_id,@project_id,@chat_id,'Getting started with Aristotle',true) RETURNING *;

-- name: ListCoresearchConversations :many
SELECT c.*,p.mode,coalesce((SELECT left(m.body,240) FROM coresearch_message m WHERE m.workspace_id=c.workspace_id AND m.chat_id=c.chat_id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),'')::text AS preview
FROM coresearch_conversation c JOIN coresearch_project p ON p.workspace_id=c.workspace_id AND p.id=c.project_id
WHERE c.workspace_id=app_workspace() AND c.owner_id=app_user() ORDER BY c.updated_at DESC,c.id;

-- name: ListSharedCowriteSessions :many
SELECT p.id AS project_id,ch.id AS chat_id,p.name AS title,p.mode,p.updated_at
FROM coresearch_project p JOIN LATERAL (SELECT id FROM coresearch_chat WHERE workspace_id=p.workspace_id AND project_id=p.id ORDER BY created_at,id LIMIT 1) ch ON true
WHERE p.workspace_id=app_workspace() AND p.mode='cowrite' AND p.created_by<>app_user() ORDER BY p.updated_at DESC,p.id;

-- name: GetCoresearchTurnReceipt :one
SELECT * FROM coresearch_turn_receipt WHERE workspace_id=app_workspace() AND owner_id=app_user() AND request_id= @request_id;

-- name: InsertCoresearchTurnReceipt :exec
INSERT INTO coresearch_turn_receipt(workspace_id,owner_id,request_id,target,body,response) VALUES(app_workspace(),app_user(),@request_id,@target,@body,@response);

-- name: GetCoresearchConversation :one
SELECT * FROM coresearch_conversation WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id FOR UPDATE;

-- name: UpdateCoresearchConversation :one
UPDATE coresearch_conversation
SET title = @title, research_id = sqlc.narg(research_id),
    pinned_at = sqlc.narg(pinned_at), archived_at = sqlc.narg(archived_at)
WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id RETURNING *;

-- name: CreateCoresearchConversation :one
INSERT INTO coresearch_conversation(workspace_id,owner_id,agent_id,project_id,chat_id,title)
VALUES(app_workspace(),app_user(),@agent_id,@project_id,@chat_id,@title) RETURNING *;

-- name: SnapshotCoresearchConversation :one
UPDATE coresearch_conversation SET snapshot_harness= @harness,snapshot_model= @model,snapshot_instructions= @instructions,updated_at=now() WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id RETURNING *;

-- name: TouchCoresearchConversation :exec
UPDATE coresearch_conversation SET updated_at=now() WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id;

-- name: SetConversationChatHarness :exec
UPDATE coresearch_chat SET cli= @harness WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id;

-- name: AristotleRuntimeAvailable :one
SELECT EXISTS(SELECT 1 FROM runtime WHERE workspace_id=app_workspace() AND owner_id=app_user() AND harness= @harness AND status='online' AND last_seen_at>now()-interval '60 seconds' AND (NOT @require_healthy::boolean OR unhealthy_reason IS NULL) AND (sqlc.narg(daemon_id)::text IS NULL OR daemon_id=sqlc.narg(daemon_id)))::boolean;

-- name: SetAristotleRunModel :one
UPDATE agent_run SET requested_model= @model WHERE workspace_id=app_workspace() AND id= @id AND kind='coresearch' RETURNING *;

-- name: AristotleRuntimeModelAvailable :one
SELECT EXISTS(SELECT 1 FROM runtime r WHERE r.workspace_id=app_workspace() AND r.owner_id=app_user() AND r.harness= @harness AND r.models @> jsonb_build_array(jsonb_build_object('value', @model::text)))::boolean;

-- name: GetCoresearchConversationForChat :one
SELECT * FROM coresearch_conversation WHERE workspace_id=app_workspace() AND owner_id=app_user() AND chat_id= @chat_id FOR UPDATE;

-- name: ClaimCoresearchUpload :execrows
DELETE FROM attachment
WHERE workspace_id=app_workspace() AND uploader_id=app_user() AND id= @id AND card_id IS NULL
  AND kind='file' AND created_at>now()-interval '24 hours' AND sha256= @sha256 AND blob_key= @blob_key;

-- name: FirstCoresearchConversation :one
SELECT * FROM coresearch_conversation WHERE workspace_id=app_workspace() AND owner_id=app_user() ORDER BY created_at,id LIMIT 1;

-- name: ConvertWelcomeConversation :one
UPDATE coresearch_conversation SET is_welcome=false, title= @title, updated_at=now()
WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id AND is_welcome RETURNING *;

-- name: ConvertWelcomeProject :exec
UPDATE coresearch_project SET name= @name, mode= @mode, conversation_owner_id=CASE WHEN @mode::text='cowrite' THEN NULL ELSE app_user() END, updated_at=now()
WHERE workspace_id=app_workspace() AND id= @id;

-- name: CoresearchConversationProject :one
SELECT project_id FROM coresearch_conversation WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id;

-- name: DeleteOwnedCoresearchProject :execrows
DELETE FROM coresearch_project WHERE workspace_id=app_workspace() AND created_by=app_user() AND id= @id;

-- name: ListMobileAristotleConversations :many
SELECT c.id, c.title, p.mode, c.snapshot_harness
FROM coresearch_conversation c
JOIN agent a ON a.workspace_id=c.workspace_id AND a.id=c.agent_id
JOIN coresearch_project p ON p.workspace_id=c.workspace_id AND p.id=c.project_id
WHERE c.workspace_id=app_workspace() AND c.owner_id=app_user()
  AND a.owner_id=app_user() AND a.built_in_key='aristotle' AND c.archived_at IS NULL
ORDER BY c.updated_at DESC,c.id;

-- name: GetMobileAristotleChat :one
SELECT c.chat_id FROM coresearch_conversation c
JOIN agent a ON a.workspace_id=c.workspace_id AND a.id=c.agent_id
WHERE c.workspace_id=app_workspace() AND c.owner_id=app_user() AND c.id=$1
  AND a.owner_id=app_user() AND a.built_in_key='aristotle';
