-- name: NextCardNumber :one
UPDATE workspace SET card_counter = card_counter + 1 WHERE id = @id RETURNING card_counter;

-- A new card goes to the top of its column. NextCardNumber has already locked
-- the workspace row, so two creates never read the same minimum.
-- name: CreateCard :one
INSERT INTO card (workspace_id, number, title, question, owner_id, deadline, position,
  scope_from, scope_to, assignee_type, assignee_id, board_column, round, research_id, machine, study_cli, auditor_cli, workflow, workflow_release)
VALUES (@workspace_id, @number, @title, @question, @owner_id, @deadline,
  COALESCE((SELECT min(b.position) FROM card b WHERE b.workspace_id = @workspace_id AND b.board_column = @board_column::text), 1) - 1,
  COALESCE(NULLIF(@scope_from::text, ''), 'grounding'), COALESCE(NULLIF(@scope_to::text, ''), 'validation'),
  sqlc.narg(assignee_type), sqlc.narg(assignee_id), @board_column::text, @round, sqlc.narg(research_id), sqlc.narg(machine), sqlc.narg(study_cli), sqlc.narg(auditor_cli), sqlc.narg(workflow), sqlc.narg(workflow_release))
RETURNING *;

-- name: CardWorkflow :one
SELECT workflow FROM card WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertCardTeam :exec
INSERT INTO card_team (workspace_id, card_id, board_column, pack_id)
VALUES (@workspace_id, @card_id, @board_column, @pack_id);

-- name: CardTeams :many
SELECT t.board_column, t.pack_id, p.name, p.stage
FROM card_team t JOIN field_pack p ON p.workspace_id = t.workspace_id AND p.id = t.pack_id
WHERE t.workspace_id = @workspace_id AND t.card_id = @card_id;

-- name: SetCardTeam :exec
INSERT INTO card_team (workspace_id, card_id, board_column, pack_id)
VALUES (@workspace_id, @card_id, @board_column, @pack_id)
ON CONFLICT (card_id, board_column) DO UPDATE SET pack_id = EXCLUDED.pack_id;

-- The board's cards. queued: an agent run of the card waits with a reason
-- (budget or runtime_offline). role: the role of its latest queued,
-- dispatched or running agent run, empty when it has none. updated_at: the
-- card's newest event, or its creation when it has none. waiting: why the
-- card's first waiting run waits on a machine (agent_run_waiting); empty
-- otherwise.
-- name: ListCards :many
SELECT c.*,
  COALESCE((SELECT w FROM agent_run t, agent_run_waiting(t.workspace_id, t.id) w
    WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id AND t.status = 'queued' AND w IS NOT NULL
    ORDER BY t.created_at, t.id LIMIT 1), '')::text AS waiting,
  EXISTS (SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
    AND t.status = 'queued' AND t.queue_reason IS NOT NULL)::boolean AS queued,
  EXISTS (SELECT 1 FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
    AND t.status = 'running')::boolean AS agent_running,
  COALESCE((SELECT t.role FROM agent_run t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id
    AND t.status IN ('queued', 'dispatched', 'running') ORDER BY t.created_at DESC, t.id DESC LIMIT 1), '')::text AS role,
  GREATEST(c.created_at, (SELECT max(e.created_at) FROM event e WHERE e.workspace_id = c.workspace_id AND e.card_id = c.id))::timestamptz AS updated_at
FROM card c WHERE c.workspace_id = @workspace_id
ORDER BY c.board_column, c.position, c.number;

-- name: GetCard :one
SELECT * FROM card WHERE workspace_id = @workspace_id AND id = @id;

-- name: CreateComment :one
INSERT INTO comment (workspace_id, card_id, author_id, body, reply_run_id, steer_attempt)
VALUES (@workspace_id, @card_id, @author_id, @body, sqlc.narg(reply_run_id), sqlc.narg(steer_attempt))
RETURNING *;

-- name: ListComments :many
SELECT c.* FROM comment c WHERE c.workspace_id = @workspace_id AND c.card_id = @card_id AND NOT EXISTS (SELECT 1 FROM comment_cancellation x WHERE x.workspace_id = c.workspace_id AND x.comment_id = c.id) ORDER BY c.created_at;

-- A study's comments posted after since that no run has been given yet.
-- name: UndeliveredComments :many
SELECT c.* FROM comment c WHERE c.workspace_id = @workspace_id AND c.card_id = @card_id AND c.created_at > @since AND NOT EXISTS (SELECT 1 FROM comment_cancellation x WHERE x.workspace_id = c.workspace_id AND x.comment_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM record r WHERE r.workspace_id = c.workspace_id AND r.card_id = c.card_id
    AND r.kind = 'comment_delivery' AND r.body->>'comment_id' = c.id::text)
ORDER BY c.created_at, c.id;

-- name: CommentDeliveries :many
SELECT body FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'comment_delivery'
ORDER BY created_at, id;

-- A stopped study needs nothing more from anyone: its open items asking for
-- a person are resolved and cancelled, whoever they were addressed to.
-- name: ResolveCardInbox :many
UPDATE inbox_item SET resolved_at = now(), status = 'cancelled', touched_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind <> 'manual_decision' AND kind IN ('escalated', 'blocked', 'scoring_unavailable') AND resolved_at IS NULL
RETURNING id;

-- What a held study waits on: the text of its open item for a person.
-- name: CardHoldText :one
SELECT text FROM inbox_item
WHERE workspace_id = @workspace_id AND card_id = @card_id AND resolved_at IS NULL
  AND (kind = 'blocked' OR kind = 'needs_decision' AND cause IN ('could_not_do', 'all_withdrawn', 'validation_invalid'))
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteCard :exec
DELETE FROM card WHERE id = @id AND workspace_id = @workspace_id;

-- A card's runs with what the study page shows about each: the name of the
-- agent it was started as, the latest text of its current attempt, when
-- that attempt last recorded anything, how each earlier attempt ended and on
-- which machine, why the run waits on a machine (agent_run_waiting), and the
-- answer of its latest hand-in.
-- name: CardAgentRuns :many
SELECT t.*, a.name AS agent_name,
  COALESCE(agent_run_excerpt(t.workspace_id, t.id, t.attempt, t.harness = 'antigravity'), '')::text AS latest_text,
  m.created_at AS last_activity_at,
  COALESCE((SELECT jsonb_agg(jsonb_build_object('attempt', rc.attempt, 'failure_reason', COALESCE(rc.result->>'failure_reason', ''),
      'machine', COALESCE(rc.result->>'machine_name', rt.custom_name, rt.device_name, '')) ORDER BY rc.attempt)
    FROM agent_run_receipt rc LEFT JOIN runtime rt ON rt.workspace_id = rc.workspace_id AND rt.id = rc.runtime_id
    WHERE rc.workspace_id = t.workspace_id AND rc.agent_run_id = t.id AND rc.attempt < t.attempt), '[]')::jsonb AS attempt_ends,
  COALESCE(agent_run_waiting(t.workspace_id, t.id), '')::text AS waiting,
  COALESCE(h.body->'hand_in'->>'answer', '')::text AS answer,
  h.body->'hand_in' AS hand_in,
  COALESCE(h.id::text, '')::text AS hand_in_id,
  CASE WHEN h.body->>'message_seq' ~ '^[0-9]{1,9}$'
    THEN (h.body->>'message_seq')::int ELSE 0 END::int AS hand_in_message_seq
FROM agent_run t
LEFT JOIN LATERAL (
  SELECT r.id, r.body FROM record r
  WHERE r.workspace_id = t.workspace_id AND r.card_id = t.card_id AND r.agent_run_id = t.id
    AND r.kind = 'hand_in' AND r.superseded_at IS NULL AND r.body->>'attempt' = t.attempt::text
  ORDER BY r.created_at DESC LIMIT 1
) h ON true
LEFT JOIN agent a ON a.workspace_id = t.workspace_id AND a.id = t.agent_id
LEFT JOIN agent_run_message m ON m.workspace_id = t.workspace_id AND m.agent_run_id = t.id AND m.attempt = t.attempt
  AND m.seq = (SELECT max(seq) FROM agent_run_message n WHERE n.workspace_id = t.workspace_id AND n.agent_run_id = t.id AND n.attempt = t.attempt)
WHERE t.workspace_id = @workspace_id AND t.card_id = @card_id ORDER BY t.created_at, t.id;

-- Every record of a card's column, superseded ones included, for the card page.
-- name: ListAllRecords :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
ORDER BY created_at, length(key), key;

-- name: SetCardMachine :execrows
UPDATE card SET machine = sqlc.narg(machine), version = version + 1
WHERE id = @id AND workspace_id = @workspace_id;
