-- name: DismissVideo :exec
-- Idempotent: dismissing an already-dismissed video keeps the original time,
-- so a double tap does not look like a fresh decision.
INSERT INTO dismissed_videos (user_id, video_id)
VALUES ($1, $2)
ON CONFLICT (user_id, video_id) DO NOTHING;

-- name: UndismissVideo :exec
DELETE FROM dismissed_videos WHERE user_id = $1 AND video_id = $2;

-- name: ListDismissedForVideos :many
-- Which of these videos the user has dismissed — one round trip per page,
-- the same shape as ListWatchEventsForVideos.
SELECT video_id FROM dismissed_videos
WHERE user_id = $1 AND video_id = ANY(sqlc.arg(video_ids)::text[]);

-- name: ListDismissed :many
-- Newest first, for the screen that lets a viewer put one back.
SELECT video_id FROM dismissed_videos
WHERE user_id = $1
ORDER BY dismissed_at DESC
LIMIT sqlc.arg(lim);
