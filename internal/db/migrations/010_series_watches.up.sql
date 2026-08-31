-- "Tell me when this channel starts a new series." A feed can watch a
-- channel: playlists TubeArchivist indexes for it later are announced once
-- in that feed, until the viewer subscribes the series (makes it a playlist
-- source somewhere) or dismisses it.
CREATE TABLE feed_series_watches (
    feed_id    uuid NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    channel_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (feed_id, channel_id)
);

-- Playlists the user already knows about, per channel: the baseline snapshot
-- taken when a watch is created, plus every announcement acted on since
-- (dismissed, or subscribed anywhere). The announcement set is TA's playlists
-- for the channel minus these rows. User-scoped, not feed-scoped, so the same
-- series never announces twice across feeds. playlist_id is a TA id and
-- deliberately not a foreign key (TA owns those).
CREATE TABLE series_seen (
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel_id  text NOT NULL,
    playlist_id text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, playlist_id)
);
CREATE INDEX idx_series_seen_user_channel ON series_seen (user_id, channel_id);
