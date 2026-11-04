-- name: InsertLabAgentRun :one
INSERT INTO agent_run(workspace_id,kind,requester_id,responsible_user_id,lab_chat_id,agent_id,board_column,round,role,harness,prompt,skill,requested_model,continuity_scope,created_at,lab_purpose)
SELECT c.workspace_id,'aristotle',c.owner_id,c.owner_id,c.id,sqlc.narg(agent_id),'',0,'aristotle',@harness,'',@skill,@model,'lab-chat:'||c.id::text||CASE WHEN c.purpose='answer' THEN '' ELSE ':'||c.purpose END,clock_timestamp(),c.purpose
FROM lab_chat c WHERE c.deleted_at IS NULL AND c.workspace_id=app_workspace() AND c.owner_id=app_user() AND c.owner_id= @asker::uuid AND c.id= @chat_id::uuid
AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=c.workspace_id AND m.user_id=c.owner_id)
RETURNING *;

-- name: InsertLabQuestion :exec
INSERT INTO lab_message(workspace_id,owner_id,chat_id,kind,body,context_card_id,context_ref,agent_run_id,created_at)
VALUES(app_workspace(),app_user(),@chat_id,'question',@body,sqlc.narg(context_card_id),sqlc.narg(context_ref),sqlc.narg(agent_run_id),clock_timestamp());

-- Only the current question enters a queued/claimed prompt. The launch request
-- builds the prior conversation after rechecking every evidence pin.
-- name: RefreshLabRunPrompt :one
UPDATE agent_run r SET prompt=q.body||
 CASE WHEN q.context_ref IS NOT NULL THEN E'\n\nContext record (read first): '||q.context_ref WHEN q.context_card_id IS NULL THEN '' ELSE E'\n\nContext study: study:'||q.context_card_id::text END
FROM lab_message q WHERE r.workspace_id=app_workspace() AND r.kind='aristotle' AND r.id= @id
AND q.workspace_id=r.workspace_id AND q.owner_id=app_user() AND q.chat_id=r.lab_chat_id AND q.agent_run_id=r.id AND q.kind='question'
RETURNING r.*;

-- name: LabPriorTurns :many
SELECT m.id, m.kind, prior_q.launch_sources, CASE WHEN m.kind='question' THEN m.body ELSE '' END::text AS question, m.proposal, m.context_card_id, m.statements, m.evidence, COALESCE(m.history_safe,false)::boolean AS history_safe
FROM agent_run r JOIN lab_message m ON m.workspace_id=r.workspace_id AND m.chat_id=r.lab_chat_id
 AND m.owner_id=r.requester_id
LEFT JOIN agent_run o ON o.workspace_id=m.workspace_id AND o.id=m.agent_run_id
LEFT JOIN lab_message prior_q ON prior_q.workspace_id=m.workspace_id AND prior_q.agent_run_id=m.agent_run_id
 AND prior_q.chat_id=m.chat_id AND prior_q.kind='question'
WHERE r.id=sqlc.arg(run_id)::uuid AND r.kind='aristotle' AND r.workspace_id=app_workspace()
 AND r.requester_id=app_user() AND m.kind IN ('question','answer')
 AND ((o.created_at,o.id)<(r.created_at,r.id) OR (o.id IS NULL AND m.created_at<r.created_at))
ORDER BY COALESCE(o.created_at,m.created_at),o.id,m.created_at,m.id;

-- name: RecordLabLaunchSources :execrows
UPDATE lab_message SET launch_sources=sqlc.arg(sources)::uuid[], launch_attempt=(SELECT attempt FROM agent_run WHERE workspace_id=app_workspace() AND id=sqlc.arg(run_id)::uuid)
WHERE workspace_id=app_workspace() AND owner_id=app_user() AND kind='question' AND agent_run_id=sqlc.arg(run_id)::uuid;

-- name: LabLaunchedSources :one
SELECT CASE WHEN m.launch_attempt=r.attempt THEN m.launch_sources ELSE NULL::uuid[] END::uuid[] AS sources
FROM lab_message m JOIN agent_run r ON r.workspace_id=m.workspace_id AND r.id=m.agent_run_id
WHERE m.workspace_id=app_workspace() AND m.owner_id=app_user() AND m.kind='question' AND m.agent_run_id=sqlc.arg(run_id)::uuid;

-- name: MarkLabAttemptStale :execrows
UPDATE lab_message m SET stale_attempt=r.attempt
FROM agent_run r WHERE r.id=sqlc.arg(run_id)::uuid AND r.workspace_id=app_workspace()
 AND r.requester_id=app_user() AND r.status='running' AND r.kind='aristotle'
 AND m.workspace_id=r.workspace_id AND m.owner_id=r.requester_id AND m.chat_id=r.lab_chat_id
 AND m.agent_run_id=r.id AND m.kind='question';

-- name: LabAttemptNeedsRestart :one
SELECT EXISTS(SELECT 1 FROM agent_run r JOIN lab_message m ON m.workspace_id=r.workspace_id
 AND m.agent_run_id=r.id AND m.chat_id=r.lab_chat_id AND m.owner_id=r.requester_id
 WHERE r.id=sqlc.arg(run_id)::uuid AND r.workspace_id=app_workspace() AND r.requester_id=app_user()
 AND r.kind='aristotle' AND m.kind='question' AND m.stale_attempt=r.attempt)::boolean;

-- name: LockDepartingLabRuns :many
SELECT * FROM agent_run
WHERE workspace_id = @workspace_id AND kind = 'aristotle' AND requester_id = @requester_id
 AND status IN ('queued','dispatched','running')
ORDER BY id FOR UPDATE;

-- name: LabChatHasPendingRuns :one
SELECT EXISTS (SELECT 1 FROM agent_run WHERE workspace_id=app_workspace() AND requester_id=app_user()
  AND kind='aristotle' AND lab_chat_id = sqlc.arg(chat_id)::uuid AND status IN ('queued','dispatched','running'));

-- name: OpenLabRunIDs :many
SELECT id FROM agent_run
WHERE workspace_id = @workspace_id AND kind = 'aristotle'
  AND status IN ('queued','dispatched','running')
ORDER BY id;
