-- name: ListBranding :many
-- What is already known about these videos. The page asks once for all of them.
SELECT * FROM dearrow_branding WHERE video_id = ANY(sqlc.arg(video_ids)::text[]);

-- name: UpsertBranding :exec
INSERT INTO dearrow_branding (
    video_id, title, original_title_won, thumbnail_time,
    original_thumb_won, random_time, has_submission, fetched_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (video_id) DO UPDATE
SET title              = EXCLUDED.title,
    original_title_won = EXCLUDED.original_title_won,
    thumbnail_time     = EXCLUDED.thumbnail_time,
    original_thumb_won = EXCLUDED.original_thumb_won,
    random_time        = EXCLUDED.random_time,
    has_submission     = EXCLUDED.has_submission,
    fetched_at         = now();
