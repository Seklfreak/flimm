-- name: GetWatchEvent :one
SELECT * FROM watch_events WHERE user_id = $1 AND video_id = $2;

-- name: ListWatchEventsForVideos :many
SELECT * FROM watch_events WHERE user_id = $1 AND video_id = ANY(sqlc.arg(video_ids)::text[]);

-- name: UpsertProgress :one
-- Heartbeat: creates the event on first play, then moves the position and
-- last_played_at. A completed event stays completed unless the caller passes
-- completed = true again; completion is only cleared by SetWatched(false).
-- A hidden (deleted-from-history) entry resurfaces on the next play.
INSERT INTO watch_events (user_id, video_id, channel_id, channel_name, title, position, duration, completed_at, hidden)
VALUES ($1, $2, $3, $4, $5, $6, $7, CASE WHEN sqlc.arg(completed)::bool THEN now() END, false)
ON CONFLICT (user_id, video_id) DO UPDATE
SET position = EXCLUDED.position,
    duration = EXCLUDED.duration,
    channel_id = EXCLUDED.channel_id,
    channel_name = EXCLUDED.channel_name,
    title = EXCLUDED.title,
    last_played_at = now(),
    completed_at = CASE WHEN sqlc.arg(completed)::bool THEN COALESCE(watch_events.completed_at, now()) ELSE watch_events.completed_at END,
    hidden = false
RETURNING *;

-- name: SetWatched :one
-- Explicit watched toggle. true completes (keeping position); false clears
-- both completion and position. Does not bump last_played_at, so toggling
-- from a list doesn't reorder history.
INSERT INTO watch_events (user_id, video_id, channel_id, channel_name, title, duration, position, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, 0, CASE WHEN sqlc.arg(watched)::bool THEN now() END)
ON CONFLICT (user_id, video_id) DO UPDATE
SET completed_at = CASE WHEN sqlc.arg(watched)::bool THEN COALESCE(watch_events.completed_at, now()) END,
    position = CASE WHEN sqlc.arg(watched)::bool THEN watch_events.position ELSE 0 END,
    duration = CASE WHEN EXCLUDED.duration > 0 THEN EXCLUDED.duration ELSE watch_events.duration END
RETURNING *;

-- name: ResetPosition :exec
-- "Start over": position back to 0, completion untouched.
UPDATE watch_events SET position = 0 WHERE user_id = $1 AND video_id = $2;

-- name: ListHistory :many
SELECT * FROM watch_events
WHERE user_id = sqlc.arg(user_id)
  AND NOT hidden
  AND (sqlc.arg(filter)::text = 'all'
       OR (sqlc.arg(filter)::text = 'seen' AND completed_at IS NOT NULL)
       OR (sqlc.arg(filter)::text = 'in_progress' AND completed_at IS NULL))
  AND (completed_at IS NOT NULL OR position >= sqlc.arg(min_position)::float8)
  AND (sqlc.arg(q)::text = ''
       OR title ILIKE '%' || sqlc.arg(q)::text || '%'
       OR channel_name ILIKE '%' || sqlc.arg(q)::text || '%')
ORDER BY last_played_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountHistory :one
SELECT count(*) FROM watch_events
WHERE user_id = sqlc.arg(user_id)
  AND NOT hidden
  AND (sqlc.arg(filter)::text = 'all'
       OR (sqlc.arg(filter)::text = 'seen' AND completed_at IS NOT NULL)
       OR (sqlc.arg(filter)::text = 'in_progress' AND completed_at IS NULL))
  AND (completed_at IS NOT NULL OR position >= sqlc.arg(min_position)::float8)
  AND (sqlc.arg(q)::text = ''
       OR title ILIKE '%' || sqlc.arg(q)::text || '%'
       OR channel_name ILIKE '%' || sqlc.arg(q)::text || '%');

-- name: HideHistoryEntry :execrows
UPDATE watch_events SET hidden = true WHERE id = $1 AND user_id = $2;

-- name: ListInProgress :many
-- "Continue watching": started, not finished, newest activity first.
SELECT * FROM watch_events
WHERE user_id = $1 AND NOT hidden AND completed_at IS NULL AND position > 0
ORDER BY last_played_at DESC
LIMIT $2;
