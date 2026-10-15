-- name: NextCardNumber :one
UPDATE workspace SET card_counter = card_counter + 1 WHERE id = @id RETURNING card_counter;

-- A new card goes to the top of Backlog, so WIP pull takes it next unless
-- someone reorders. NextCardNumber has already locked the workspace row, so two
-- creates never read the same minimum.
-- name: CreateCard :one
INSERT INTO card (workspace_id, number, title, question, owner_id, budget_usd, deadline, follows_from, position)
VALUES (@workspace_id, @number, @title, @question, @owner_id, @budget_usd, @deadline, @follows_from,
  COALESCE((SELECT min(b.position) FROM card b WHERE b.workspace_id = @workspace_id AND b.board_column = 'backlog'), 1) - 1)
RETURNING *;

-- name: ListCards :many
SELECT * FROM card WHERE workspace_id = @workspace_id ORDER BY number;

-- name: GetCard :one
SELECT * FROM card WHERE workspace_id = @workspace_id AND id = @id;

-- name: CreateComment :one
INSERT INTO comment (workspace_id, card_id, author_id, body) VALUES (@workspace_id, @card_id, @author_id, @body)
RETURNING *;

-- name: ListComments :many
SELECT * FROM comment WHERE workspace_id = @workspace_id AND card_id = @card_id ORDER BY created_at;

-- name: ListInbox :many
SELECT * FROM inbox_item WHERE workspace_id = @workspace_id AND user_id = @user_id
ORDER BY created_at DESC LIMIT 200;

-- name: MarkInboxRead :execrows
UPDATE inbox_item SET read_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND user_id = @user_id AND read_at IS NULL;

-- People edit and delete a card only while it is in Backlog; the column check
-- is repeated here so a card WIP pull has started is never changed.
-- name: EditBacklogCard :one
UPDATE card SET title = @title, question = @question, budget_usd = @budget_usd, deadline = sqlc.narg(deadline),
  follows_from = @follows_from, position = @position, version = version + 1
WHERE id = @id AND workspace_id = @workspace_id AND board_column = 'backlog'
RETURNING *;

-- name: DeleteBacklogCard :execrows
DELETE FROM card WHERE id = @id AND workspace_id = @workspace_id AND board_column = 'backlog';
