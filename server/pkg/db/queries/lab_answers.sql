-- name: LabTurnHasAnswer :one
SELECT lab_has_accepted_answer(sqlc.arg(run_id)::uuid)::boolean;

-- name: LabEvidenceLog :many
SELECT ref,via,(CASE WHEN min(read_version)=max(read_version) THEN min(read_version) ELSE '' END)::text AS read_version
FROM lab_read WHERE workspace_id=app_workspace() AND agent_run_id=sqlc.arg(run_id)::uuid
 AND attempt=(SELECT r.attempt FROM agent_run r WHERE r.workspace_id=app_workspace() AND r.id=sqlc.arg(run_id)::uuid)
 GROUP BY ref,via ORDER BY min(id);

-- name: RecordLabAnswer :one
SELECT recorded.id::uuid AS id FROM lab_record_answer(sqlc.arg(run_id)::uuid,@answer::jsonb) AS recorded(id);

-- name: RecordLabFailure :one
INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body,agent_run_id)
SELECT r.workspace_id,c.id,c.owner_id,'failure',@body,r.id
FROM agent_run r JOIN lab_chat c ON (c.workspace_id,c.id,c.owner_id)=(r.workspace_id,r.lab_chat_id,r.requester_id)
WHERE r.id=sqlc.arg(run_id)::uuid AND r.workspace_id=app_workspace() AND r.requester_id=app_user()
AND c.deleted_at IS NULL AND r.kind='aristotle' AND r.status IN ('failed','cancelled')
ON CONFLICT DO NOTHING RETURNING id;
