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
