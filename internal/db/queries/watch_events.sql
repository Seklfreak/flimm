-- name: GetWatchEvent :one
SELECT * FROM watch_events WHERE user_id = $1 AND video_id = $2;

-- name: ListWatchEventsForVideos :many
SELECT * FROM watch_events WHERE user_id = $1 AND video_id = ANY(sqlc.arg(video_ids)::text[]);

-- name: UpsertProgress :one
-- Heartbeat: creates the event on first play, then moves the position and
-- last_played_at.
--
-- Completion follows the *current* watch. `completed` sets it (keeping the
-- first completion's timestamp); `restart` clears it, which is what a video
-- being watched again from the start looks like — without that, a video seen
-- once could never hold a resume position again, and every client started it
-- from zero forever. The caller decides what counts as a restart, so a video
-- opened by accident does not undo having seen it; see postProgress.
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
    completed_at = CASE
        WHEN sqlc.arg(completed)::bool THEN COALESCE(watch_events.completed_at, now())
        WHEN sqlc.arg(restart)::bool THEN NULL
        ELSE watch_events.completed_at
    END,
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
WHERE watch_events.user_id = sqlc.arg(user_id)
  AND NOT hidden
  AND (sqlc.arg(filter)::text = 'all'
       OR (sqlc.arg(filter)::text = 'seen' AND completed_at IS NOT NULL)
       OR (sqlc.arg(filter)::text = 'in_progress' AND completed_at IS NULL))
  -- "In progress" is the queue of what to resume, and "not interested" takes
  -- a video out of every queue; the full history still lists it, marked,
  -- which is where a viewer finds one again.
  AND (sqlc.arg(filter)::text <> 'in_progress'
       OR NOT EXISTS (SELECT 1 FROM dismissed_videos d
                      WHERE d.user_id = watch_events.user_id AND d.video_id = watch_events.video_id))
  AND (completed_at IS NOT NULL OR position >= sqlc.arg(min_position)::float8)
  AND (sqlc.arg(q)::text = ''
       OR title ILIKE '%' || sqlc.arg(q)::text || '%'
       OR channel_name ILIKE '%' || sqlc.arg(q)::text || '%')
ORDER BY last_played_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountHistory :one
SELECT count(*) FROM watch_events
WHERE watch_events.user_id = sqlc.arg(user_id)
  AND NOT hidden
  AND (sqlc.arg(filter)::text = 'all'
       OR (sqlc.arg(filter)::text = 'seen' AND completed_at IS NOT NULL)
       OR (sqlc.arg(filter)::text = 'in_progress' AND completed_at IS NULL))
  -- "In progress" is the queue of what to resume, and "not interested" takes
  -- a video out of every queue; the full history still lists it, marked,
  -- which is where a viewer finds one again.
  AND (sqlc.arg(filter)::text <> 'in_progress'
       OR NOT EXISTS (SELECT 1 FROM dismissed_videos d
                      WHERE d.user_id = watch_events.user_id AND d.video_id = watch_events.video_id))
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

-- Stats: what a viewer's watch history adds up to.
--
-- One row per (user, video), so "seconds" is the furthest point reached in
-- each video — a finished video counts its whole duration, an abandoned one
-- counts where it stopped, and neither counts a rewatch. It is what the table
-- can honestly answer; see docs/api.md "Watch stats".

-- name: WatchTotals :one
SELECT
    count(*)                                                     AS started,
    count(*) FILTER (WHERE completed_at IS NOT NULL)             AS finished,
    coalesce(sum(least(
        CASE WHEN completed_at IS NOT NULL THEN duration::float8 ELSE position END,
        CASE WHEN duration > 0 THEN duration::float8 ELSE position END
    )), 0)::float8                                               AS seconds,
    min(first_played_at)::timestamptz                            AS since
FROM watch_events
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(since)::timestamptz IS NULL OR first_played_at >= sqlc.arg(since)::timestamptz);

-- name: WatchTopChannels :many
SELECT
    channel_id,
    max(channel_name)::text                                      AS channel_name,
    count(*)                                                     AS videos,
    coalesce(sum(least(
        CASE WHEN completed_at IS NOT NULL THEN duration::float8 ELSE position END,
        CASE WHEN duration > 0 THEN duration::float8 ELSE position END
    )), 0)::float8                                               AS seconds
FROM watch_events
WHERE user_id = sqlc.arg(user_id)
  AND channel_id <> ''
  AND (sqlc.arg(since)::timestamptz IS NULL OR first_played_at >= sqlc.arg(since)::timestamptz)
GROUP BY channel_id
ORDER BY seconds DESC, videos DESC
LIMIT sqlc.arg(row_limit);

-- name: WatchByHour :many
-- When videos are *started*, in the viewer's own timezone — the caller passes
-- the zone because the server's is nobody's business.
SELECT
    EXTRACT(HOUR FROM first_played_at AT TIME ZONE sqlc.arg(zone)::text)::int AS hour,
    count(*)                                                                  AS videos
FROM watch_events
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(since)::timestamptz IS NULL OR first_played_at >= sqlc.arg(since)::timestamptz)
GROUP BY 1
ORDER BY 1;

-- name: WatchByWeekday :many
SELECT
    EXTRACT(ISODOW FROM first_played_at AT TIME ZONE sqlc.arg(zone)::text)::int AS weekday,
    count(*)                                                                    AS videos
FROM watch_events
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(since)::timestamptz IS NULL OR first_played_at >= sqlc.arg(since)::timestamptz)
GROUP BY 1
ORDER BY 1;

-- name: WatchByMonth :many
-- The last `months` calendar months of activity, oldest first.
SELECT
    to_char(first_played_at AT TIME ZONE sqlc.arg(zone)::text, 'YYYY-MM')     AS month,
    count(*)                                                                  AS videos,
    coalesce(sum(least(
        CASE WHEN completed_at IS NOT NULL THEN duration::float8 ELSE position END,
        CASE WHEN duration > 0 THEN duration::float8 ELSE position END
    )), 0)::float8                                                            AS seconds
FROM watch_events
WHERE user_id = sqlc.arg(user_id)
  AND first_played_at >= sqlc.arg(since)::timestamptz
GROUP BY 1
ORDER BY 1;

-- name: ListFinishedVideos :many
-- Of these videos, the ones nobody is part-way through: completed by at least
-- one viewer and in progress for none of them.
--
-- It asks about a given set rather than listing the whole history because the
-- caller starts from what is on the disk, which is far smaller. `hidden` is
-- deliberately ignored: deleting a video from your history says nothing about
-- whether you finished it, and a hidden row still holds the position that says
-- someone is mid-way.
SELECT DISTINCT w.video_id FROM watch_events w
WHERE w.video_id = ANY(sqlc.arg(video_ids)::text[])
  AND w.completed_at IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM watch_events o
      WHERE o.video_id = w.video_id
        AND o.completed_at IS NULL
        AND o.position > 0
  );
