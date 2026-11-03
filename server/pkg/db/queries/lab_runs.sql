-- name: InsertLabAgentRun :one
INSERT INTO agent_run(workspace_id,kind,requester_id,responsible_user_id,lab_chat_id,agent_id,board_column,round,role,harness,prompt,skill,requested_model,continuity_scope,created_at)
SELECT c.workspace_id,'aristotle',c.owner_id,c.owner_id,c.id,sqlc.narg(agent_id),'',0,'aristotle',@harness,'',@skill,@model,'lab-chat:'||c.id::text,clock_timestamp()
FROM lab_chat c WHERE c.workspace_id=app_workspace() AND c.owner_id=app_user() AND c.owner_id= @asker::uuid AND c.id= @chat_id::uuid
AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=c.workspace_id AND m.user_id=c.owner_id)
RETURNING *;

-- name: InsertLabQuestion :exec
INSERT INTO lab_message(workspace_id,owner_id,chat_id,kind,body,context_card_id,agent_run_id,created_at)
VALUES(app_workspace(),app_user(),@chat_id,'question',@body,sqlc.narg(context_card_id),sqlc.narg(agent_run_id),clock_timestamp());

-- An earlier turn can finish after this question was sent. Refresh at dispatch
-- so even a fresh CLI session sees earlier answers, without seeing later turns.
-- name: RefreshLabRunPrompt :one
UPDATE agent_run r SET prompt=q.body||
 CASE WHEN q.context_card_id IS NULL THEN '' ELSE E'\n\nContext study: study:'||q.context_card_id::text END||
 COALESCE((SELECT E'\n\nEarlier conversation:\n'||string_agg(m.kind||': '||m.body||
 CASE WHEN m.kind='answer' AND m.statements<>'[]'::jsonb THEN E'\n'||m.statements::text ELSE '' END,E'\n' ORDER BY COALESCE(o.created_at,m.created_at),o.id,m.created_at,m.id)
 FROM lab_message m LEFT JOIN agent_run o ON o.workspace_id=m.workspace_id AND o.id=m.agent_run_id
 WHERE m.workspace_id=q.workspace_id AND m.chat_id=q.chat_id AND m.owner_id=q.owner_id
 AND m.kind IN ('question','answer') AND ((o.created_at,o.id)<(r.created_at,r.id) OR (o.id IS NULL AND m.created_at<q.created_at))), '')
FROM lab_message q WHERE r.workspace_id=app_workspace() AND r.kind='aristotle' AND r.id= @id
AND q.workspace_id=r.workspace_id AND q.owner_id=app_user() AND q.chat_id=r.lab_chat_id AND q.agent_run_id=r.id AND q.kind='question'
RETURNING r.*;

-- name: LockDepartingLabRuns :many
SELECT * FROM agent_run
WHERE workspace_id = @workspace_id AND kind = 'aristotle' AND requester_id = @requester_id
 AND status IN ('queued','dispatched','running')
ORDER BY id FOR UPDATE;
