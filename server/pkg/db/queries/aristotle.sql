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
VALUES(app_workspace(),app_user(),@agent_id,@project_id,@chat_id,'Welcome to Aristotle',true) RETURNING *;

-- name: ListCoresearchConversations :many
SELECT c.*,coalesce((SELECT left(m.body,240) FROM coresearch_message m WHERE m.workspace_id=c.workspace_id AND m.chat_id=c.chat_id ORDER BY m.created_at DESC,m.id DESC LIMIT 1),'')::text AS preview
FROM coresearch_conversation c WHERE c.workspace_id=app_workspace() AND c.owner_id=app_user() ORDER BY c.updated_at DESC,c.id;
