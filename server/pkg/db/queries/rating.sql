-- name: RatingDrawn :one
SELECT EXISTS (SELECT 1 FROM team_rating WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column);

-- Whether the user was offered a rating in this workspace after since.
-- name: OfferedSince :one
SELECT EXISTS (SELECT 1 FROM team_rating WHERE workspace_id = @workspace_id AND user_id = @user_id AND offered AND drawn_at > @since);

-- name: InsertRatingDraw :exec
INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at)
VALUES (@workspace_id, @card_id, @board_column, @pack_id, @user_id, @offered, @drawn_at)
ON CONFLICT (card_id, board_column) DO NOTHING;

-- The user's offers on one study, in column order.
-- name: CardRatings :many
SELECT * FROM team_rating WHERE workspace_id = @workspace_id AND card_id = @card_id AND user_id = @user_id AND offered
ORDER BY array_position(ARRAY['grounding', 'hypotheses', 'experimentation', 'validation'], board_column);

-- name: SetRating :one
UPDATE team_rating SET rating = sqlc.narg(rating), rated_at = @rated_at
WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column AND user_id = @user_id AND offered
RETURNING *;

-- name: DismissRating :one
UPDATE team_rating SET dismissed_at = @dismissed_at
WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column AND user_id = @user_id AND offered
RETURNING *;

-- name: RatingOwner :one
SELECT user_id FROM team_rating WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column AND offered;
