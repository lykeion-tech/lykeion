-- The people a finished study's report goes to: its owner, then everyone who
-- commented on it or decided one of its Inbox items, those still members.
-- name: StudyParticipants :many
SELECT m.user_id FROM member m
WHERE m.workspace_id = @workspace_id AND (
  m.user_id = (SELECT c.owner_id FROM card c WHERE c.workspace_id = @workspace_id AND c.id = @card_id)
  OR m.user_id IN (SELECT cm.author_id FROM comment cm WHERE cm.workspace_id = @workspace_id AND cm.card_id = @card_id)
  OR m.user_id IN (SELECT i.decided_by FROM inbox_item i WHERE i.workspace_id = @workspace_id AND i.card_id = @card_id AND i.decided_by IS NOT NULL))
ORDER BY m.user_id = (SELECT c.owner_id FROM card c WHERE c.workspace_id = @workspace_id AND c.id = @card_id) DESC, m.user_id;
