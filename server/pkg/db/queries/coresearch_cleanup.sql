-- name: EnqueueCoresearchProjectBlobs :many
SELECT public.coresearch_enqueue_project_blobs(@project_id::uuid)::text AS blob_key;

-- name: CoresearchBlobCleanupWorkspaces :many
SELECT public.coresearch_blob_cleanup_workspaces()::uuid AS workspace_id;

-- name: LockCoresearchBlobCleanup :exec
SELECT pg_advisory_xact_lock(@lock_id::bigint);

-- name: PendingCoresearchBlobCleanup :many
SELECT blob_key FROM coresearch_blob_cleanup WHERE workspace_id=app_workspace() ORDER BY created_at,blob_key LIMIT 128;

-- name: LockCoresearchBlobCleanupKey :one
SELECT blob_key FROM coresearch_blob_cleanup WHERE workspace_id=app_workspace() AND blob_key= @blob_key FOR UPDATE;

-- name: CoresearchCleanupBlobReferenced :one
SELECT public.coresearch_cleanup_blob_referenced(@blob_key::text)::boolean AS referenced;

-- name: RemoveCoresearchBlobCleanup :exec
DELETE FROM coresearch_blob_cleanup WHERE workspace_id=app_workspace() AND blob_key= @blob_key;
