-- One table for everything Flimm asks a third party.
--
-- The DeArrow cache (006) proved the shape: serve what is known immediately,
-- refresh what is stale behind the response, and only ever wait for something
-- nothing is known about. SponsorBlock and Return YouTube Dislike have exactly
-- the same problem — a lookup on somebody else's service, inside a request,
-- behind a cache that used to live in memory and die on every deploy — so they
-- get the same answer rather than a third copy of it.
--
-- The payload is opaque on purpose. Nothing here is ever queried by its
-- contents, only by its key, and the three sources hold different shapes: a
-- title and a thumbnail time, a list of segments, a pair of vote counts. What
-- each one *means* stays in the code that asked for it.
--
-- `has_data` is the one thing worth knowing about a payload without opening it:
-- whether the service had anything to say. It decides how long the row stays
-- fresh, because "nobody has submitted anything" is most of this table and the
-- least likely to change.
CREATE TABLE external_cache (
    source     text        NOT NULL,
    key        text        NOT NULL,
    payload    jsonb       NOT NULL,
    has_data   boolean     NOT NULL DEFAULT false,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source, key)
);

-- The sweep asks for the oldest rows of one source.
CREATE INDEX idx_external_cache_fetched_at ON external_cache (source, fetched_at);

-- Carry the DeArrow rows over rather than re-fetching eight thousand videos:
-- the table was filled by asking a service that is slow when it is unhappy, and
-- throwing that away would mean asking again for no reason.
INSERT INTO external_cache (source, key, payload, has_data, fetched_at)
SELECT
    'dearrow',
    video_id,
    jsonb_strip_nulls(jsonb_build_object(
        'title', title,
        'original_title_won', original_title_won,
        'thumbnail_time', thumbnail_time,
        'original_thumb_won', original_thumb_won,
        'random_time', random_time
    )),
    has_submission,
    fetched_at
FROM dearrow_branding
ON CONFLICT (source, key) DO NOTHING;

DROP TABLE dearrow_branding;
