-- name: ListPinnedPlaylists :many
SELECT * FROM pinned_playlists WHERE user_id = $1 ORDER BY position, created_at;

-- name: PinPlaylist :exec
-- Appends to the end of the user's pins. Re-pinning an already pinned
-- playlist keeps its position rather than moving it to the end.
INSERT INTO pinned_playlists (user_id, playlist_id, position)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(playlist_id),
    COALESCE((SELECT max(position) + 1 FROM pinned_playlists WHERE user_id = sqlc.arg(user_id)), 0)
)
ON CONFLICT (user_id, playlist_id) DO NOTHING;

-- name: UnpinPlaylist :exec
DELETE FROM pinned_playlists WHERE user_id = $1 AND playlist_id = $2;
