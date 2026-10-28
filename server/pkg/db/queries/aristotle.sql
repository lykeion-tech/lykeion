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
SELECT c.*,coalesce((SELECT left(m.body,240) FROM coresearch_message m WHERE m.workspace_id=c.workspace_id AND m.chat_id=c.chat_id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),'')::text AS preview
FROM coresearch_conversation c WHERE c.workspace_id=app_workspace() AND c.owner_id=app_user() ORDER BY c.updated_at DESC,c.id;

-- name: GetCoresearchTurnReceipt :one
SELECT * FROM coresearch_turn_receipt WHERE workspace_id=app_workspace() AND owner_id=app_user() AND request_id= @request_id;

-- name: InsertCoresearchTurnReceipt :exec
INSERT INTO coresearch_turn_receipt(workspace_id,owner_id,request_id,target,body,response) VALUES(app_workspace(),app_user(),@request_id,@target,@body,@response);

-- name: GetCoresearchConversation :one
SELECT * FROM coresearch_conversation WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id FOR UPDATE;

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
SELECT EXISTS(SELECT 1 FROM runtime WHERE workspace_id=app_workspace() AND owner_id=app_user() AND harness= @harness AND status='online' AND last_seen_at>now()-interval '60 seconds' AND (sqlc.narg(daemon_id)::text IS NULL OR daemon_id=sqlc.narg(daemon_id)))::boolean;

-- name: SetAristotleRunModel :one
UPDATE agent_run SET requested_model= @model WHERE workspace_id=app_workspace() AND id= @id AND kind='coresearch' RETURNING *;
