-- name: NextCardNumber :one
UPDATE workspace SET card_counter = card_counter + 1 WHERE id = @id RETURNING card_counter;

-- A new card goes to the top of Backlog, so WIP pull takes it next unless
-- someone reorders. NextCardNumber has already locked the workspace row, so two
-- creates never read the same minimum.
-- name: CreateCard :one
INSERT INTO card (workspace_id, number, title, question, owner_id, deadline, follows_from, position,
  scope_from, scope_to, assignee_type, assignee_id, board_column, round, research_id)
VALUES (@workspace_id, @number, @title, @question, @owner_id, @deadline, @follows_from,
  COALESCE((SELECT min(b.position) FROM card b WHERE b.workspace_id = @workspace_id AND b.board_column = 'backlog'), 1) - 1,
  COALESCE(NULLIF(@scope_from::text, ''), 'grounding'), COALESCE(NULLIF(@scope_to::text, ''), 'reporting'),
  sqlc.narg(assignee_type), sqlc.narg(assignee_id), COALESCE(NULLIF(@board_column::text, ''), 'backlog'), @round, sqlc.narg(research_id))
RETURNING *;

-- name: InsertCardTeam :exec
INSERT INTO card_team (workspace_id, card_id, board_column, pack_id)
VALUES (@workspace_id, @card_id, @board_column, @pack_id);

-- name: CardTeams :many
SELECT t.board_column, t.pack_id, p.name, p.version, p.stage
FROM card_team t JOIN field_pack p ON p.workspace_id = t.workspace_id AND p.id = t.pack_id
WHERE t.workspace_id = @workspace_id AND t.card_id = @card_id;

-- name: SetCardTeam :exec
INSERT INTO card_team (workspace_id, card_id, board_column, pack_id)
VALUES (@workspace_id, @card_id, @board_column, @pack_id)
ON CONFLICT (card_id, board_column) DO UPDATE SET pack_id = EXCLUDED.pack_id;

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
UPDATE inbox_item SET read_at = COALESCE(read_at, now()),
  resolved_at = CASE WHEN kind IN ('done', 'middle_score', 'no_tool', 'autoresearch_card', 'autoresearch_report') THEN COALESCE(resolved_at, now()) ELSE resolved_at END
WHERE id = @id AND workspace_id = @workspace_id AND user_id = @user_id
  AND (read_at IS NULL OR (kind IN ('done', 'middle_score', 'no_tool', 'autoresearch_card', 'autoresearch_report') AND resolved_at IS NULL))
RETURNING *;

-- A stopped study needs nothing more from anyone: its open items asking for
-- a person are resolved, whoever they were addressed to.
-- name: ResolveCardInbox :many
UPDATE inbox_item SET resolved_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind IN ('escalated', 'blocked', 'scoring_unavailable') AND resolved_at IS NULL
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

-- A card's runs with what the study page shows about each: the name of the
-- agent it was started as, the latest text of its current attempt and when
-- that attempt last recorded anything.
-- name: CardAgentRuns :many
SELECT t.*, a.name AS agent_name,
  COALESCE(agent_run_excerpt(t.workspace_id, t.id, t.attempt, t.harness = 'antigravity'), '')::text AS latest_text,
  m.created_at AS last_activity_at
FROM agent_run t
LEFT JOIN agent a ON a.workspace_id = t.workspace_id AND a.id = t.agent_id
LEFT JOIN agent_run_message m ON m.workspace_id = t.workspace_id AND m.agent_run_id = t.id AND m.attempt = t.attempt
  AND m.seq = (SELECT max(seq) FROM agent_run_message n WHERE n.workspace_id = t.workspace_id AND n.agent_run_id = t.id AND n.attempt = t.attempt)
WHERE t.workspace_id = @workspace_id AND t.card_id = @card_id ORDER BY t.created_at, t.id;

-- Every record of a card's column, superseded ones included, for the card page.
-- name: ListAllRecords :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
ORDER BY created_at, length(key), key;
