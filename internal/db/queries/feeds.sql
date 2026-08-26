-- name: ListFeeds :many
SELECT * FROM feeds WHERE user_id = $1 ORDER BY position, created_at;

-- name: GetFeed :one
SELECT * FROM feeds WHERE id = $1 AND user_id = $2;

-- name: CreateFeed :one
INSERT INTO feeds (user_id, name, sort, hide_seen, include_shorts, subtitles_only, pinned, position)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateFeed :one
UPDATE feeds
SET name = $3,
    sort = $4,
    hide_seen = $5,
    include_shorts = $6,
    subtitles_only = $7,
    pinned = $8,
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
