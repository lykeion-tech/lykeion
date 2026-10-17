-- name: NextCardNumber :one
UPDATE workspace SET card_counter = card_counter + 1 WHERE id = @id RETURNING card_counter;

-- A new card goes to the top of Backlog, so WIP pull takes it next unless
-- someone reorders. NextCardNumber has already locked the workspace row, so two
-- creates never read the same minimum.
-- name: CreateCard :one
INSERT INTO card (workspace_id, number, title, question, owner_id, deadline, follows_from, position)
VALUES (@workspace_id, @number, @title, @question, @owner_id, @deadline, @follows_from,
  COALESCE((SELECT min(b.position) FROM card b WHERE b.workspace_id = @workspace_id AND b.board_column = 'backlog'), 1) - 1)
RETURNING *;

-- The board's cards. queued: an agent run of the card waits with a reason
-- (budget or runtime_offline). role: the role of its latest queued,
-- dispatched or running agent run, empty when it has none. updated_at: the
-- card's newest event, or its creation when it has none.
-- name: ListCards :many
SELECT c.*,
  EXISTS (SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
    AND t.status = 'queued' AND t.queue_reason IS NOT NULL)::boolean AS queued,
  COALESCE((SELECT t.role FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
    AND t.status IN ('queued', 'dispatched', 'running') ORDER BY t.created_at DESC, t.id DESC LIMIT 1), '')::text AS role,
  GREATEST(c.created_at, (SELECT max(e.created_at) FROM event e WHERE e.workspace_id = c.workspace_id AND e.card_id = c.id))::timestamptz AS updated_at
FROM card c WHERE c.workspace_id = @workspace_id
ORDER BY c.board_column, c.position, c.number;

-- name: GetCard :one
SELECT * FROM card WHERE workspace_id = @workspace_id AND id = @id;

-- name: CreateComment :one
INSERT INTO comment (workspace_id, card_id, author_id, body) VALUES (@workspace_id, @card_id, @author_id, @body)
RETURNING *;

-- name: ListComments :many
SELECT * FROM comment WHERE workspace_id = @workspace_id AND card_id = @card_id ORDER BY created_at;

-- name: ListInbox :many
SELECT * FROM inbox_item WHERE workspace_id = @workspace_id AND user_id = @user_id AND resolved_at IS NULL
ORDER BY created_at DESC LIMIT 200;

-- name: ListArchivedInbox :many
SELECT * FROM inbox_item WHERE workspace_id = @workspace_id AND user_id = @user_id AND resolved_at IS NOT NULL
ORDER BY created_at DESC, id DESC LIMIT 200;

-- Reading informational notifications also resolves them.
-- name: MarkInboxRead :one
UPDATE inbox_item SET read_at = COALESCE(read_at, now()), resolved_at = CASE WHEN kind IN ('released', 'autoresearch_card', 'autoresearch_report') THEN now() ELSE resolved_at END
WHERE id = @id AND workspace_id = @workspace_id AND user_id = @user_id
  AND (read_at IS NULL OR (kind IN ('autoresearch_card', 'autoresearch_report') AND resolved_at IS NULL))
RETURNING *;

-- A person answered a blocked card: its open escalated items are resolved,
-- whoever they were addressed to.
-- name: ResolveCardInbox :many
UPDATE inbox_item SET resolved_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'escalated' AND resolved_at IS NULL
RETURNING id;

-- People edit and delete a card only while it is in Backlog; the column check
-- is repeated here so a card WIP pull has started is never changed.
-- name: EditBacklogCard :one
UPDATE card SET title = @title, question = @question, deadline = sqlc.narg(deadline),
  follows_from = @follows_from, position = @position, version = version + 1
WHERE id = @id AND workspace_id = @workspace_id AND board_column = 'backlog'
RETURNING *;

-- name: DeleteBacklogCard :execrows
DELETE FROM card WHERE id = @id AND workspace_id = @workspace_id AND board_column = 'backlog';

-- name: CardAgentRuns :many
SELECT * FROM agent_run WHERE workspace_id = @workspace_id AND card_id = @card_id ORDER BY created_at, id;

-- Every record of a card's column, superseded ones included, for the card page.
-- name: ListAllRecords :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
ORDER BY created_at;
