-- Videos the user has taken out of their feeds without watching them.
--
-- Deliberately not the `hidden` flag on watch_events: that one means "removed
-- from history" and comes back on the next play, and it only exists for a
-- video that was played at all. This is the opposite case — a video the user
-- has decided not to watch — so it needs a row of its own, and marking one
-- dismissed must never imply anything about watch state.
--
-- video_id is a TubeArchivist id and is not a foreign key: TA owns those, and
-- a video deleted there should simply stop resolving rather than break a write
-- here (the same reasoning as pinned_playlists).
CREATE TABLE dismissed_videos (
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video_id     text NOT NULL,
    dismissed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);
-- Feed listings ask "which of these ids are dismissed" for a page at a time.
CREATE INDEX idx_dismissed_videos_user ON dismissed_videos (user_id, dismissed_at DESC);
