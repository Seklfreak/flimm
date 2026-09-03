-- name: UpsertPushDevice :exec
-- A token is a device; registering it again (a new launch, a token Apple
-- rotated, a different account on the same phone) moves it to the caller.
INSERT INTO push_devices (token, user_id, platform, environment)
VALUES ($1, $2, $3, $4)
ON CONFLICT (token) DO UPDATE
SET user_id = EXCLUDED.user_id,
    platform = EXCLUDED.platform,
    environment = EXCLUDED.environment,
    updated_at = now();

-- name: DeletePushDevice :execrows
-- User-scoped: signing out removes the caller's own registration only.
DELETE FROM push_devices WHERE token = $1 AND user_id = $2;

-- name: ForgetPushDevice :exec
-- Server-side: APNs said the token is dead, whoever it belonged to.
DELETE FROM push_devices WHERE token = $1;

-- name: ListPushDevices :many
SELECT * FROM push_devices WHERE user_id = $1 ORDER BY created_at;

-- name: CountPushDevices :one
SELECT COUNT(*) FROM push_devices WHERE user_id = $1;

-- name: ListNotifyFeeds :many
-- Every feed of every user that asked to be notified — what the notifier
-- walks each pass.
SELECT * FROM feeds WHERE notify ORDER BY user_id, position;

-- name: SetFeedNotifiedAt :exec
UPDATE feeds SET notified_at = $2 WHERE id = $1;
