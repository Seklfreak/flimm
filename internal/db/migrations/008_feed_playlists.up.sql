-- A feed's sources were whole channels only. A channel that publishes
-- everything into series (playlists) wants finer grain: put *one series* in a
-- feed without following the rest of the channel. So a feed now also holds
-- playlists, and its videos are the union of both source kinds.
--
-- A separate table rather than a kind column on feed_channels: every consumer
-- (membership badges, unseen counts, the stream composer) works per kind, and
-- the two id namespaces never mix in a query.
--
-- playlist_id is a TubeArchivist id and deliberately not a foreign key: TA
-- owns playlists, and one deleted there should simply stop resolving rather
-- than break a write here (same reasoning as playlist_settings).
CREATE TABLE feed_playlists (
    feed_id     uuid NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    playlist_id text NOT NULL,
    position    integer NOT NULL DEFAULT 0,
    PRIMARY KEY (feed_id, playlist_id)
);
CREATE INDEX idx_feed_playlists_playlist ON feed_playlists (playlist_id);
