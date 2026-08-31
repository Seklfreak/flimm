-- Channels the user pinned to the sidebar, the same per-user state pinned
-- playlists started as (002). A separate table rather than a kind column on
-- playlist_settings: the two id namespaces never mix in a query, and a
-- channel has no second flag (music is a playlist notion).
--
-- channel_id is a TubeArchivist id and deliberately not a foreign key: TA
-- owns channels, and one deleted there should simply stop resolving rather
-- than break a write here.
CREATE TABLE pinned_channels (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel_id text NOT NULL,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel_id)
);
CREATE INDEX idx_pinned_channels_user_position ON pinned_channels (user_id, position);
