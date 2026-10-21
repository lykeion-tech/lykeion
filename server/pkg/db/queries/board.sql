-- name: LockCard :one
SELECT * FROM card WHERE id = @id AND workspace_id = @workspace_id FOR NO KEY UPDATE;

-- name: TryLockCard :one
SELECT pg_try_advisory_xact_lock(hashtextextended(sqlc.arg(card_id)::uuid::text, 1)) AS ok;

-- The same lock as TryLockCard, waited for: the engine's write phase must not
-- drop finished scoring because a short read phase holds the lock.
-- name: WaitLockCard :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(card_id)::uuid::text, 1));

-- Sets or clears the card's scoring lease. version is left alone: the lease is
-- the engine's bookkeeping, not a change to the card.
-- name: SetGatingLease :exec
UPDATE card SET gating_until = sqlc.narg(gating_until) WHERE id = @id AND workspace_id = @workspace_id;

-- spent_usd is never written here: F2's settle is its only writer.
-- name: SaveCardBoard :exec
UPDATE card SET board_column = @board_column, state = @state, flag = @flag, ending = @ending,
  end_reason = @end_reason, rounds = @rounds,
  round = @round, handoff_note = @handoff_note, block_kind = @block_kind, question = @question, version = version + 1
WHERE id = @id AND workspace_id = @workspace_id;

-- name: RoundAgentRuns :many
SELECT * FROM agent_run WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid
  AND board_column = @board_column AND round = @round ORDER BY created_at;

-- The live records each agent run of a round wrote itself, so the engine can tell
-- whether a role left a result.
-- name: RoundAgentRunRecords :many
SELECT agent_run_id::uuid AS agent_run_id, kind, body FROM record
WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
  AND round = @round AND agent_run_id IS NOT NULL AND superseded_at IS NULL;

-- name: SetAgentRunHandoffNote :exec
UPDATE agent_run SET handoff_note = @handoff_note WHERE id = @id AND workspace_id = @workspace_id;

-- Which CLI session a role run continues on its owner's daemon.
-- name: SetAgentRunContinuity :exec
UPDATE agent_run SET continuity_scope = @continuity_scope WHERE id = @id AND workspace_id = @workspace_id;

-- Sets or clears why a card's queued agent runs wait. Clearing also drops
-- runtime_offline, which the next tick restores when it still holds.
-- name: SetCardQueueReason :exec
UPDATE agent_run SET queue_reason = sqlc.narg(queue_reason)
WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid AND status = 'queued';

-- name: MarkQueueOffline :exec
SELECT mark_queue_offline();

-- A study still open asks the question: not stopped, and not done.
-- name: ActiveDuplicate :one
SELECT id, number FROM card
WHERE workspace_id = @workspace_id AND ending = '' AND state <> 'done'
  AND (norm_text(title) = norm_text(@title::text) OR norm_text(question) = norm_text(@question::text))
ORDER BY number LIMIT 1;

-- name: GetCardByNumber :one
SELECT * FROM card WHERE workspace_id = @workspace_id AND number = @number;

-- name: CountKindRecords :one
SELECT count(*) FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id
  AND board_column = @board_column AND kind = @kind;

-- name: SupersedeRoleKinds :exec
UPDATE record SET superseded_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
  AND role = @role AND kind = ANY(@kinds::text[]) AND superseded_at IS NULL;

-- name: InsertInboxItem :exec
INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
VALUES (@workspace_id, @card_id::uuid, @user_id, @kind, @text);

-- name: ActiveCardCount :one
SELECT count(*) FROM card WHERE workspace_id = @workspace_id AND ending = ''
  AND board_column NOT IN ('backlog', 'done');

-- WIP pull takes the top of Backlog first: the lowest position, then the lowest number.
-- name: BacklogCards :many
SELECT * FROM card WHERE workspace_id = @workspace_id AND board_column = 'backlog' AND flag = ''
ORDER BY position, number;

-- name: BoardCandidates :many
SELECT workspace_id::uuid AS workspace_id, card_id::uuid AS card_id, board_column::text AS board_column
FROM board_candidates();

-- name: SetWorkspaceSettings :exec
UPDATE workspace SET settings = @settings WHERE id = @id;

-- name: CardInflightAgentRuns :many
SELECT id FROM agent_run
WHERE workspace_id = @workspace_id AND card_id = @card_id
  AND status IN ('queued', 'dispatched', 'running')
ORDER BY created_at, id;

-- The live records of one kind on a card, in every column, oldest first.
-- name: CardKindRecords :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = @kind
  AND superseded_at IS NULL ORDER BY created_at, id;

-- name: LiveCardRecord :one
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND id = @id
  AND superseded_at IS NULL;

-- name: SupersedeRecord :exec
UPDATE record SET superseded_at = now() WHERE workspace_id = @workspace_id AND id = @id AND superseded_at IS NULL;
