-- name: GetAccountResearchProfile :one
SELECT d.profile AS draft, s.profile AS published,
       ARRAY(SELECT g.workspace_id FROM research_profile_publication g
             WHERE g.user_id = d.user_id ORDER BY g.workspace_id)::uuid[] AS workspace_ids
FROM research_profile_draft d
LEFT JOIN research_profile_snapshot s ON s.user_id = d.user_id
WHERE d.user_id = @user_id;

-- name: SaveResearchProfileDraft :exec
INSERT INTO research_profile_draft (user_id, profile)
VALUES (@user_id, @profile)
ON CONFLICT (user_id) DO UPDATE SET profile = EXCLUDED.profile;

-- name: LockResearchProfileDraft :one
SELECT user_id FROM research_profile_draft WHERE user_id = @user_id FOR UPDATE;

-- name: InitializeResearchProfileSnapshot :exec
INSERT INTO research_profile_snapshot (user_id, profile)
SELECT d.user_id, d.profile FROM research_profile_draft d WHERE d.user_id = @user_id
ON CONFLICT (user_id) DO NOTHING;

-- name: PublishResearchProfileChanges :execrows
UPDATE research_profile_snapshot s SET profile = d.profile
FROM research_profile_draft d
WHERE s.user_id = d.user_id AND s.user_id = @user_id AND EXISTS (
  SELECT 1 FROM research_profile_publication g WHERE g.user_id = s.user_id
);

-- name: ShareResearchProfile :execrows
INSERT INTO research_profile_publication (workspace_id, user_id)
VALUES (@workspace_id, @user_id)
ON CONFLICT (workspace_id, user_id) DO NOTHING;

-- name: UnshareResearchProfile :execrows
DELETE FROM research_profile_publication WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: GetMemberResearchProfile :one
SELECT p.profile
FROM member m
LEFT JOIN research_profile_publication g ON g.workspace_id = m.workspace_id AND g.user_id = m.user_id
LEFT JOIN research_profile_snapshot p ON p.user_id = g.user_id
WHERE m.workspace_id = @workspace_id AND m.user_id = @user_id
  AND m.workspace_id = app_workspace() AND EXISTS (
    SELECT 1 FROM member caller WHERE caller.workspace_id = m.workspace_id AND caller.user_id = app_user()
  );

-- name: ListResearchDirectory :many
SELECT m.user_id, u.name, p.profile
FROM member m
JOIN "user" u ON u.id = m.user_id
LEFT JOIN research_profile_publication g ON g.workspace_id = m.workspace_id AND g.user_id = m.user_id
LEFT JOIN research_profile_snapshot p ON p.user_id = g.user_id
WHERE m.workspace_id = @workspace_id
  AND m.workspace_id = app_workspace() AND EXISTS (
    SELECT 1 FROM member caller WHERE caller.workspace_id = m.workspace_id AND caller.user_id = app_user()
  )
ORDER BY m.user_id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');
