-- name: ListCached :many
-- What is already known for these keys. One query per page, per source.
SELECT * FROM external_cache
WHERE source = sqlc.arg(source) AND key = ANY(sqlc.arg(keys)::text[]);

-- name: UpsertCached :exec
INSERT INTO external_cache (source, key, payload, has_data, fetched_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (source, key) DO UPDATE
SET payload    = EXCLUDED.payload,
    has_data   = EXCLUDED.has_data,
    fetched_at = now();
