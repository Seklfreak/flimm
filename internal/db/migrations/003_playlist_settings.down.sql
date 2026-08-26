DELETE FROM playlist_settings WHERE NOT pinned;
ALTER TABLE playlist_settings DROP COLUMN audio_only;
ALTER TABLE playlist_settings DROP COLUMN pinned;
ALTER INDEX idx_playlist_settings_user_position RENAME TO idx_pinned_playlists_user_position;
ALTER TABLE playlist_settings RENAME TO pinned_playlists;
