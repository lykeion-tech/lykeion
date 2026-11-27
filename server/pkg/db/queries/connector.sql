-- name: GetWorkspaceConnector :one
SELECT * FROM workspace_connector WHERE workspace_id = @workspace_id AND name = @name;

-- name: ListWorkspaceConnectors :many
SELECT * FROM workspace_connector WHERE workspace_id = @workspace_id;

-- name: FreshConnectorFetch :one
SELECT * FROM connector_fetch
WHERE workspace_id = @workspace_id AND request_sha256 = @request_sha256 AND fetched_at > now() - interval '1 day';

-- name: PutConnectorFetch :exec
INSERT INTO connector_fetch (workspace_id, request_sha256, connector, blob_key, content_type, truncated)
VALUES (@workspace_id, @request_sha256, @connector, @blob_key, @content_type, @truncated)
ON CONFLICT (workspace_id, request_sha256) DO UPDATE
SET connector = EXCLUDED.connector, blob_key = EXCLUDED.blob_key, content_type = EXCLUDED.content_type,
    truncated = EXCLUDED.truncated, fetched_at = now();

-- The Team that works one column of a study.
-- name: GetCardTeamPack :one
SELECT pack_id FROM card_team WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column;

-- The last call number of a study: calls count across its columns and
-- rounds, superseded ones included. Callers hold the card lock (LockRole).
-- name: LastCallNumber :one
SELECT COALESCE(max(substr(key, 2)::int), 0)::int AS n FROM record
WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = 'call' AND key ~ '^c[0-9]+$';

-- name: EnsureWorkspaceConnector :exec
INSERT INTO workspace_connector (workspace_id, name, updated_by)
VALUES (@workspace_id, @name, @updated_by)
ON CONFLICT (workspace_id, name) DO NOTHING;

-- name: LockWorkspaceConnector :one
SELECT * FROM workspace_connector WHERE workspace_id = @workspace_id AND name = @name FOR UPDATE;
