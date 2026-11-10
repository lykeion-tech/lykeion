-- The people a finished study's report goes to: its owner, then everyone who
-- commented on it or decided one of its Inbox items, those still members.
-- name: StudyParticipants :many
SELECT m.user_id FROM member m
WHERE m.workspace_id = @workspace_id AND (
  m.user_id = (SELECT c.owner_id FROM card c WHERE c.workspace_id = @workspace_id AND c.id = @card_id)
  OR m.user_id IN (SELECT cm.author_id FROM comment cm WHERE cm.workspace_id = @workspace_id AND cm.card_id = @card_id)
  OR m.user_id IN (SELECT i.decided_by FROM inbox_item i WHERE i.workspace_id = @workspace_id AND i.card_id = @card_id AND i.decided_by IS NOT NULL))
ORDER BY m.user_id = (SELECT c.owner_id FROM card c WHERE c.workspace_id = @workspace_id AND c.id = @card_id) DESC, m.user_id;

-- Every run a study made, oldest first.
-- name: StudyRuns :many
SELECT * FROM agent_run WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid ORDER BY created_at, id;

-- The name of each agent that ran in a study.
-- name: StudyRunAgents :many
SELECT a.id, a.name FROM agent a
WHERE a.workspace_id = @workspace_id
  AND a.id IN (SELECT r.agent_id FROM agent_run r WHERE r.workspace_id = @workspace_id AND r.card_id = @card_id::uuid);

-- A study's Inbox items someone decided, oldest decision first.
-- name: StudyDecidedItems :many
SELECT * FROM inbox_item WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid AND decided_by IS NOT NULL
ORDER BY decided_at, created_at, id;
