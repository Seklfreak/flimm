-- name: UpsertUser :one
-- Called on every authenticated request from the JWT's sub/email/name. The sub
-- is the stable identity; email/name are refreshed and last_seen_at bumped.
INSERT INTO users (oidc_sub, email, name, last_seen_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (oidc_sub) DO UPDATE
SET email = EXCLUDED.email,
    name = EXCLUDED.name,
    last_seen_at = now()
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetPrefs :one
SELECT prefs FROM user_prefs WHERE user_id = $1;

-- name: UpsertPrefs :exec
INSERT INTO user_prefs (user_id, prefs, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (user_id) DO UPDATE
SET prefs = EXCLUDED.prefs,
    updated_at = now();
