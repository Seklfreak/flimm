-- name: ListPlaylistSettings :many
SELECT * FROM playlist_settings WHERE user_id = $1;

-- name: ListPinnedPlaylists :many
SELECT * FROM playlist_settings
WHERE user_id = $1 AND pinned
ORDER BY position, created_at;

-- name: SetPlaylistPinned :exec
-- Appends to the end of the user's pins on first insert; re-pinning an
-- already pinned playlist keeps its position rather than moving it.
INSERT INTO playlist_settings (user_id, playlist_id, pinned, position)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(playlist_id),
    sqlc.arg(pinned),
    COALESCE((SELECT max(position) + 1 FROM playlist_settings WHERE user_id = sqlc.arg(user_id)), 0)
)
ON CONFLICT (user_id, playlist_id) DO UPDATE SET pinned = EXCLUDED.pinned;

-- name: SetPlaylistAudioOnly :exec
INSERT INTO playlist_settings (user_id, playlist_id, audio_only, position)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(playlist_id),
    sqlc.arg(audio_only),
    COALESCE((SELECT max(position) + 1 FROM playlist_settings WHERE user_id = sqlc.arg(user_id)), 0)
)
ON CONFLICT (user_id, playlist_id) DO UPDATE SET audio_only = EXCLUDED.audio_only;

-- name: PruneEmptyPlaylistSettings :exec
-- A row with nothing set is noise; drop it so the table only holds intent.
DELETE FROM playlist_settings WHERE user_id = $1 AND NOT pinned AND NOT audio_only;
