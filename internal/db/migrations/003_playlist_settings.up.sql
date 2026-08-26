-- Pinning was the first per-playlist setting; audio-only is the second, and
-- they are independent (a playlist can be audio-only without being pinned).
-- Generalise the table rather than adding a second one keyed identically.
ALTER TABLE pinned_playlists RENAME TO playlist_settings;
ALTER INDEX idx_pinned_playlists_user_position RENAME TO idx_playlist_settings_user_position;

-- Default false so rows created for audio-only don't silently become pins;
-- every row that already exists was a pin, so backfill those.
ALTER TABLE playlist_settings ADD COLUMN pinned boolean NOT NULL DEFAULT false;
UPDATE playlist_settings SET pinned = true;

ALTER TABLE playlist_settings ADD COLUMN audio_only boolean NOT NULL DEFAULT false;
