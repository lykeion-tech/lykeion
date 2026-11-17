-- name: UpsertUserByEmail :one
INSERT INTO "user" (email) VALUES (@email)
ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
RETURNING *;

-- name: UpsertUserByGoogle :one
INSERT INTO "user" (email, name, google_sub) VALUES (@email, @name, @google_sub)
ON CONFLICT (email) DO UPDATE SET google_sub = EXCLUDED.google_sub,
  name = CASE WHEN "user".name = '' THEN EXCLUDED.name ELSE "user".name END
RETURNING *;

-- name: GetUser :one
SELECT * FROM "user" WHERE id = @id;

-- Changes only the fields given; onboarded stamps onboarded_at once and never clears it.
-- name: UpdateMe :one
UPDATE "user" SET name = COALESCE(sqlc.narg(name), name), timezone = COALESCE(sqlc.narg(timezone), timezone),
  career_stage = COALESCE(sqlc.narg(career_stage), career_stage), avatar_url = COALESCE(sqlc.narg(avatar_url), avatar_url),
  onboarded_at = CASE WHEN @onboarded::boolean THEN COALESCE(onboarded_at, now()) ELSE onboarded_at END
WHERE id = @id
RETURNING *;

-- name: SetVerificationCode :exec
INSERT INTO verification_code (email, code_hash, expires_at) VALUES (@email, @code_hash, @expires_at)
ON CONFLICT (email) DO UPDATE SET code_hash = EXCLUDED.code_hash, expires_at = EXCLUDED.expires_at, attempts = 0;

-- name: GetVerificationCode :one
SELECT * FROM verification_code WHERE email = @email;

-- name: BumpVerificationAttempts :exec
UPDATE verification_code SET attempts = attempts + 1 WHERE email = @email;

-- name: DeleteVerificationCode :exec
DELETE FROM verification_code WHERE email = @email;

-- name: CreateToken :one
INSERT INTO personal_access_token (user_id, token_hash, prefix, name, expires_at)
VALUES (@user_id, @token_hash, @prefix, @name, @expires_at)
RETURNING id, prefix, name, expires_at, created_at;

-- name: TokenByHash :one
SELECT t.user_id, u.email FROM personal_access_token t JOIN "user" u ON u.id = t.user_id
WHERE t.token_hash = @token_hash AND t.revoked_at IS NULL AND t.expires_at > now();

-- name: ListTokens :many
SELECT id, prefix, name, expires_at, revoked_at, created_at FROM personal_access_token
WHERE user_id = @user_id ORDER BY created_at DESC;

-- name: RevokeToken :execrows
UPDATE personal_access_token SET revoked_at = now()
WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL;

-- name: SetUserTypesafeKey :exec
INSERT INTO user_typesafe_key (user_id, ciphertext) VALUES (@user_id, @ciphertext)
ON CONFLICT (user_id) DO UPDATE SET ciphertext = EXCLUDED.ciphertext, updated_at = now();

-- name: ClearUserTypesafeKey :exec
DELETE FROM user_typesafe_key WHERE user_id = @user_id;

-- name: GetUserTypesafeKey :one
SELECT ciphertext FROM user_typesafe_key WHERE user_id = @user_id;
