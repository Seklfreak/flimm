-- What DeArrow said about a video, kept so a viewer never waits for it twice.
--
-- Crowd titles are an enhancement on a page that is otherwise ready, and they
-- were being fetched inside the request that needed them: one lookup per video,
-- an internet round trip each, on every list page. The service answers in
-- 200-400ms when healthy and has been measured at fifteen seconds when not, so
-- a page of thirty videos was routinely a second of waiting and occasionally
-- much worse.
--
-- The cache it had was in memory, which meant it died on every deploy and was
-- dropped wholesale once it passed a few thousand entries — for an archive of
-- eight thousand videos it was cold most of the time. This is that cache, kept
-- where a restart cannot reach it.
--
-- Rows are global rather than per-user: what the crowd called a video is the
-- same fact for everyone, so one lookup serves the whole deployment.
--
-- Most rows say *nothing was submitted* — around nine in ten, measured against
-- a real archive — and those are the valuable ones. They are the answer that
-- would otherwise be fetched again and again for videos nobody has ever
-- retitled.
--
-- video_id is a TubeArchivist id and is not a foreign key; TA owns those (the
-- same reasoning as dismissed_videos).
CREATE TABLE dearrow_branding (
    video_id           text PRIMARY KEY,
    -- The winning crowd title, empty when there is none.
    title              text NOT NULL DEFAULT '',
    -- The crowd voted for the video's own title; an answer, not an absence.
    original_title_won boolean NOT NULL DEFAULT false,
    -- Seconds into the video for the crowd's thumbnail, null when there is none.
    thumbnail_time     double precision,
    original_thumb_won boolean NOT NULL DEFAULT false,
    -- DeArrow's own generated suggestion, as a fraction of the video.
    random_time        double precision NOT NULL DEFAULT 0,
    -- Whether anyone has submitted anything at all. It decides how long the row
    -- stays fresh: a submission can be voted on and moves, "nobody has touched
    -- this" rarely changes and is most of the table.
    has_submission     boolean NOT NULL DEFAULT false,
    fetched_at         timestamptz NOT NULL DEFAULT now()
);

-- The sweep asks for the oldest rows first.
CREATE INDEX idx_dearrow_fetched_at ON dearrow_branding (fetched_at);
