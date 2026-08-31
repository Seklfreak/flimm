-- name: ListSeriesWatches :many
SELECT channel_id FROM feed_series_watches WHERE feed_id = $1 ORDER BY channel_id;

-- name: ListSeriesWatchesForUser :many
SELECT fw.feed_id, fw.channel_id
FROM feed_series_watches fw
JOIN feeds f ON f.id = fw.feed_id
WHERE f.user_id = $1;

-- name: AddSeriesWatch :exec
INSERT INTO feed_series_watches (feed_id, channel_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DeleteSeriesWatches :exec
DELETE FROM feed_series_watches WHERE feed_id = $1;

-- name: MarkSeriesSeen :exec
INSERT INTO series_seen (user_id, channel_id, playlist_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ListSeriesSeen :many
-- Every playlist the user already knows for the given channels.
SELECT playlist_id FROM series_seen WHERE user_id = $1 AND channel_id = ANY(sqlc.arg(channel_ids)::text[]);
