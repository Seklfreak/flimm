-- Playlists the user pinned to the sidebar. TubeArchivist has no concept of
-- pinning, so this is Archive's own state, per user. playlist_id is a TA id
-- (custom or channel playlist) and is deliberately not a foreign key: TA owns
-- those, and a playlist deleted there should simply stop resolving rather than
-- break a write here.
CREATE TABLE pinned_playlists (
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    playlist_id text NOT NULL,
    position    integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, playlist_id)
);
CREATE INDEX idx_pinned_playlists_user_position ON pinned_playlists (user_id, position);
