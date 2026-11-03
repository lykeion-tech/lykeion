-- name: LabTurnHasAnswer :one
SELECT EXISTS(SELECT 1 FROM lab_message WHERE workspace_id=app_workspace()
 AND owner_id=app_user() AND agent_run_id=sqlc.arg(run_id)::uuid AND kind='answer')::boolean;

-- name: LabEvidenceLog :many
SELECT ref,via FROM lab_read WHERE workspace_id=app_workspace() AND agent_run_id=sqlc.arg(run_id)::uuid
 GROUP BY ref,via ORDER BY min(id);

-- name: RecordLabAnswer :one
SELECT recorded.id::uuid AS id FROM lab_record_answer(sqlc.arg(run_id)::uuid,@answer::jsonb) AS recorded(id);

-- name: RecordLabFailure :one
INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body,agent_run_id)
SELECT r.workspace_id,c.id,c.owner_id,'failure',@body,r.id
FROM agent_run r JOIN lab_chat c ON (c.workspace_id,c.id,c.owner_id)=(r.workspace_id,r.lab_chat_id,r.requester_id)
WHERE r.id=sqlc.arg(run_id)::uuid AND r.workspace_id=app_workspace() AND r.requester_id=app_user()
AND r.kind='aristotle' AND r.status='failed'
ON CONFLICT DO NOTHING RETURNING id;
