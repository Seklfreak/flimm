-- name: ListPinnedChannels :many
SELECT * FROM pinned_channels WHERE user_id = $1 ORDER BY position, created_at;

-- name: PinChannel :exec
-- Appends to the end of the user's pins; re-pinning keeps the position.
INSERT INTO pinned_channels (user_id, channel_id, position)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(channel_id),
    COALESCE((SELECT max(position) + 1 FROM pinned_channels WHERE user_id = sqlc.arg(user_id)), 0)
)
ON CONFLICT (user_id, channel_id) DO NOTHING;

-- name: UnpinChannel :exec
DELETE FROM pinned_channels WHERE user_id = $1 AND channel_id = $2;
