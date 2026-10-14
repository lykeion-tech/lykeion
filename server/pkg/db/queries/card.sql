-- name: NextCardNumber :one
UPDATE workspace SET card_counter = card_counter + 1 WHERE id = @id RETURNING card_counter;

-- name: CreateCard :one
INSERT INTO card (workspace_id, number, title, question, owner_id, budget_usd, deadline, follows_from)
VALUES (@workspace_id, @number, @title, @question, @owner_id, @budget_usd, @deadline, @follows_from)
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
