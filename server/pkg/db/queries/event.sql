-- name: LockWorkspaceEvents :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(workspace_id)::uuid::text, 0));

-- name: InsertEvent :one
INSERT INTO event (workspace_id, card_id, type, actor_type, actor_id, payload)
VALUES (@workspace_id, @card_id, @type, @actor_type, @actor_id, @payload)
RETURNING *;

-- name: EventsAfter :many
SELECT * FROM event WHERE workspace_id = @workspace_id AND id > @after ORDER BY id LIMIT @lim;

-- name: CardEventsAfter :many
SELECT * FROM event WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid AND id > @after
ORDER BY id LIMIT 500;
