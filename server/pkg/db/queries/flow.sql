-- The researcher's own agent for one member of a Team version.
-- name: GetTeamAgent :one
SELECT a.* FROM team_agent t JOIN agent a ON a.workspace_id = t.workspace_id AND a.id = t.agent_id
WHERE t.workspace_id = @workspace_id AND t.pack_id = @pack_id AND t.user_id = @user_id AND t.member = @member;

-- The last record number of a study: r1, r2, … across its columns and rounds,
-- superseded ones included. Callers hold the card lock.
-- name: LastRecordNumber :one
SELECT COALESCE(max(substr(key, 2)::int), 0)::int AS n FROM record
WHERE workspace_id = @workspace_id AND card_id = @card_id AND key ~ '^r[0-9]+$';

-- name: CountRoundKindRecords :one
SELECT count(*) FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id
  AND board_column = @board_column AND round = @round AND kind = @kind;

-- Which of keys name a live record of the study.
-- name: LiveRecordKeys :many
SELECT DISTINCT key FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id
  AND key = ANY(@keys::text[]) AND superseded_at IS NULL;

-- name: AgentRunRecords :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id
ORDER BY created_at, id;

-- name: LiveRecordsByKeys :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id
  AND key = ANY(@keys::text[]) AND superseded_at IS NULL ORDER BY created_at, id;

-- name: CountOpenCardInbox :one
SELECT count(*) FROM inbox_item WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid
  AND kind = @kind AND resolved_at IS NULL;

-- name: CountOpenNoTool :one
-- An open no_tool item's text is a fixed lead, then the need it names, whole;
-- the lead's first "what it needed: " comes before anything a need can say.
SELECT count(*) FROM inbox_item WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid
  AND kind = 'no_tool' AND resolved_at IS NULL
  AND substr(text, strpos(text, 'what it needed: ') + length('what it needed: ')) = @need::text;

-- name: ResolveCardInboxKind :many
UPDATE inbox_item SET resolved_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid AND kind = @kind AND resolved_at IS NULL
RETURNING id;

-- Moves a study on, restarts its column or blocks it once the column is
-- scored, and releases the scoring lease.
-- name: SaveCardFlow :exec
UPDATE card SET board_column = @board_column, state = @state, block_kind = @block_kind, round = @round,
  redos = @redos, handoff_note = @handoff_note, gating_until = NULL, version = version + 1
WHERE id = @id AND workspace_id = @workspace_id;

-- name: LastRunNumber :one
SELECT COALESCE(max(substr(key, 2)::int), 0)::int AS n FROM record
WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'run' AND key ~ '^x[0-9]+$';

-- name: LastOutputNumber :one
SELECT COALESCE(max(substr(key, 2)::int), 0)::int AS n FROM record
WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'output' AND key ~ '^o[0-9]+$';

-- name: RunOutputTotals :one
SELECT count(*)::int AS outputs, COALESCE(sum((body->>'size_bytes')::bigint), 0)::bigint AS bytes FROM record
WHERE workspace_id = @workspace_id AND agent_run_id = @agent_run_id AND kind = 'output' AND superseded_at IS NULL;
