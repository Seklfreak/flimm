CREATE TABLE dearrow_branding (
    video_id           text PRIMARY KEY,
    title              text NOT NULL DEFAULT '',
    original_title_won boolean NOT NULL DEFAULT false,
    thumbnail_time     double precision,
    original_thumb_won boolean NOT NULL DEFAULT false,
    random_time        double precision NOT NULL DEFAULT 0,
    has_submission     boolean NOT NULL DEFAULT false,
    fetched_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_dearrow_fetched_at ON dearrow_branding (fetched_at);

INSERT INTO dearrow_branding (
    video_id, title, original_title_won, thumbnail_time,
    original_thumb_won, random_time, has_submission, fetched_at
)
SELECT
    key,
    coalesce(payload->>'title', ''),
    coalesce((payload->>'original_title_won')::boolean, false),
    (payload->>'thumbnail_time')::double precision,
    coalesce((payload->>'original_thumb_won')::boolean, false),
    coalesce((payload->>'random_time')::double precision, 0),
    has_data,
    fetched_at
FROM external_cache WHERE source = 'dearrow';

DROP TABLE external_cache;
