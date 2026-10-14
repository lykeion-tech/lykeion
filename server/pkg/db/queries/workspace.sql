-- name: CreateWorkspace :one
INSERT INTO workspace (id, slug, name) VALUES (@id, @slug, @name) RETURNING *;

-- name: ListMyWorkspaces :many
SELECT w.id, w.slug, w.name, w.created_at, m.role FROM workspace w
JOIN member m ON m.workspace_id = w.id
WHERE m.user_id = @user_id ORDER BY w.created_at;

-- name: GetWorkspace :one
SELECT * FROM workspace WHERE id = @id;

-- name: RenameWorkspace :one
UPDATE workspace SET name = @name WHERE id = @id RETURNING *;

-- name: DeleteWorkspace :exec
DELETE FROM workspace WHERE id = @id;

-- name: GetMember :one
SELECT * FROM member WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: AddMember :exec
INSERT INTO member (workspace_id, user_id, role) VALUES (@workspace_id, @user_id, @role)
ON CONFLICT (workspace_id, user_id) DO NOTHING;

-- name: ListMembers :many
SELECT m.user_id, m.role, m.created_at, u.email, u.name FROM member m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = @workspace_id ORDER BY m.created_at;

-- name: SetMemberRole :execrows
UPDATE member SET role = @role WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: RemoveMember :execrows
DELETE FROM member WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: CountOwners :one
SELECT count(*) FROM member WHERE workspace_id = @workspace_id AND role = 'owner';

-- name: CreateInvitation :one
INSERT INTO workspace_invitation (workspace_id, email, role, token_hash, invited_by, expires_at)
VALUES (@workspace_id, @email, @role, @token_hash, @invited_by, @expires_at)
RETURNING id, workspace_id, email, role, expires_at, created_at;

-- name: ListInvitations :many
SELECT id, email, role, expires_at, created_at FROM workspace_invitation
WHERE workspace_id = @workspace_id AND accepted_at IS NULL ORDER BY created_at;

-- name: RevokeInvitation :execrows
DELETE FROM workspace_invitation WHERE id = @id AND workspace_id = @workspace_id AND accepted_at IS NULL;

-- name: InvitationByToken :one
SELECT * FROM workspace_invitation
WHERE token_hash = @token_hash AND accepted_at IS NULL AND expires_at > now();

-- name: AcceptInvitation :exec
UPDATE workspace_invitation SET accepted_at = now() WHERE id = @id;

-- name: LockWorkspaceForMutation :one
SELECT id FROM workspace WHERE id = @id FOR UPDATE;

-- name: LockMember :one
SELECT * FROM member WHERE workspace_id = @workspace_id AND user_id = @user_id FOR SHARE;

-- name: SetInvitationWorkspace :exec
SELECT set_config('app.workspace_id', @workspace_id::text, true);

-- name: LockInvitationByToken :one
SELECT * FROM workspace_invitation
WHERE token_hash = @token_hash AND accepted_at IS NULL AND expires_at > now() FOR UPDATE;
