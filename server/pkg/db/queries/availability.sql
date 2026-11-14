-- name: GetWorkspaceAvailability :one
SELECT enabled,revision FROM workspace_availability WHERE workspace_id = @workspace_id AND kind = @kind AND resource_id = @resource_id;

-- name: SetWorkspaceAvailability :one
INSERT INTO workspace_availability(workspace_id,kind,resource_id,enabled,revision,updated_by)
VALUES (@workspace_id,@kind,@resource_id,@enabled,1,@updated_by)
ON CONFLICT(workspace_id,kind,resource_id) DO UPDATE SET enabled=EXCLUDED.enabled,revision=workspace_availability.revision+1,updated_by=EXCLUDED.updated_by
RETURNING enabled,revision;
