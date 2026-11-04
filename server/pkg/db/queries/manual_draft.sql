-- name: CreateManualDraftChat :one
INSERT INTO lab_chat(workspace_id,owner_id,purpose,draft)
VALUES(app_workspace(),app_user(),'manual_decision_draft','{"title":"","context":"","deadline":null,"card_id":null}') RETURNING *;

-- name: SaveManualDraft :one
UPDATE lab_chat SET draft= @draft::jsonb,updated_at=clock_timestamp()
WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id AND purpose='manual_decision_draft' AND deleted_at IS NULL RETURNING *;

-- name: SetManualDraftSource :execrows
UPDATE lab_chat SET draft_source= @source::uuid
WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id AND purpose='manual_decision_draft' AND deleted_at IS NULL;

-- name: SetManualDraftRequest :exec
UPDATE lab_message SET request_id= @request_id::uuid WHERE workspace_id=app_workspace() AND owner_id=app_user() AND agent_run_id= @run_id::uuid AND kind='question';

-- name: GetManualDraftRequest :one
SELECT m.* FROM lab_message m JOIN lab_chat c ON c.id=m.chat_id AND c.workspace_id=m.workspace_id AND c.owner_id=m.owner_id
WHERE m.workspace_id=app_workspace() AND m.owner_id=app_user() AND m.chat_id= @chat_id AND m.request_id= @request_id AND c.purpose='manual_decision_draft' AND c.deleted_at IS NULL;

-- name: GetPrivateManualDraftQuestion :one
SELECT m.* FROM lab_message m JOIN lab_chat c
 ON (c.workspace_id,c.id,c.owner_id)=(m.workspace_id,m.chat_id,m.owner_id)
WHERE m.workspace_id=app_workspace() AND m.owner_id=app_user()
 AND m.chat_id= @chat_id AND m.id= @id AND m.kind='question'
 AND c.purpose='manual_decision_draft' AND c.deleted_at IS NULL;
