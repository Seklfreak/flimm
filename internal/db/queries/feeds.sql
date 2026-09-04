-- name: ListFeeds :many
SELECT * FROM feeds WHERE user_id = $1 ORDER BY position, created_at;

-- name: GetFeed :one
SELECT * FROM feeds WHERE id = $1 AND user_id = $2;

-- name: CreateFeed :one
-- A feed born notifying starts its high-water mark at now: nothing already in
-- the archive is news.
INSERT INTO feeds (user_id, name, sort, hide_seen, include_shorts, subtitles_only, pinned, position, notify, notified_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CASE WHEN $9::boolean THEN now() END)
RETURNING *;

-- name: UpdateFeed :one
UPDATE feeds
SET name = $3,
    sort = $4,
    hide_seen = $5,
    include_shorts = $6,
    subtitles_only = $7,
    pinned = $8,
    -- Switching notifications on (re)starts the high-water mark at now;
    -- switching them off clears it, so a later switch-on cannot announce
    -- everything that arrived in between.
    notified_at = CASE
        WHEN $9::boolean AND NOT notify THEN now()
        WHEN NOT $9::boolean THEN NULL
        ELSE notified_at
    END,
    -- ...and a switch-on is re-seeded: what the sources hold at that moment
    -- is not news (see notify_seen).
    notify_seeded = CASE WHEN $9::boolean AND NOT notify THEN false ELSE notify_seeded END,
    notify = $9,
    updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: DeleteFeed :execrows
DELETE FROM feeds WHERE id = $1 AND user_id = $2;

-- name: UnpinFeeds :exec
UPDATE feeds SET pinned = false, updated_at = now() WHERE user_id = $1 AND pinned;

-- name: SetFeedPosition :exec
UPDATE feeds SET position = $3, updated_at = now() WHERE id = $1 AND user_id = $2;

-- name: NextFeedPosition :one
SELECT COALESCE(MAX(position), -1)::int + 1 FROM feeds WHERE user_id = $1;

-- name: ListFeedChannelsForUser :many
-- Every (feed, channel) membership of the user in one go, so feed lists and
-- channel "In feeds:" badges need a single query.
SELECT fc.feed_id, fc.channel_id, fc.position, f.name AS feed_name
FROM feed_channels fc
JOIN feeds f ON f.id = fc.feed_id
WHERE f.user_id = $1
ORDER BY f.position, fc.position;

-- name: ListFeedChannels :many
SELECT channel_id FROM feed_channels WHERE feed_id = $1 ORDER BY position;

-- name: DeleteFeedChannels :exec
DELETE FROM feed_channels WHERE feed_id = $1;

-- name: AddFeedChannel :exec
INSERT INTO feed_channels (feed_id, channel_id, position)
VALUES ($1, $2, $3)
ON CONFLICT (feed_id, channel_id) DO UPDATE SET position = EXCLUDED.position;

-- name: DeleteChannelFromUserFeeds :exec
-- Removes a channel from every feed the user owns (before re-adding it to the
-- selected ones).
DELETE FROM feed_channels fc
USING feeds f
WHERE fc.feed_id = f.id AND f.user_id = $1 AND fc.channel_id = $2;

-- name: NextFeedChannelPosition :one
SELECT COALESCE(MAX(position), -1)::int + 1 FROM feed_channels WHERE feed_id = $1;

-- name: ListFeedPlaylistsForUser :many
-- Every (feed, playlist) membership of the user in one go, so feed lists and
-- playlist "In feeds:" badges need a single query.
SELECT fp.feed_id, fp.playlist_id, fp.position, f.name AS feed_name
FROM feed_playlists fp
JOIN feeds f ON f.id = fp.feed_id
WHERE f.user_id = $1
ORDER BY f.position, fp.position;

-- name: ListFeedPlaylists :many
SELECT playlist_id FROM feed_playlists WHERE feed_id = $1 ORDER BY position;

-- name: DeleteFeedPlaylists :exec
DELETE FROM feed_playlists WHERE feed_id = $1;

-- name: AddFeedPlaylist :exec
INSERT INTO feed_playlists (feed_id, playlist_id, position)
VALUES ($1, $2, $3)
ON CONFLICT (feed_id, playlist_id) DO UPDATE SET position = EXCLUDED.position;

-- name: DeletePlaylistFromUserFeeds :exec
-- Removes a playlist from every feed the user owns (before re-adding it to
-- the selected ones).
DELETE FROM feed_playlists fp
USING feeds f
WHERE fp.feed_id = f.id AND f.user_id = $1 AND fp.playlist_id = $2;

-- name: NextFeedPlaylistPosition :one
SELECT COALESCE(MAX(position), -1)::int + 1 FROM feed_playlists WHERE feed_id = $1;
