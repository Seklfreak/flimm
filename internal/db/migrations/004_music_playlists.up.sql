-- The flag started as "play this playlist as audio" but now also means "this
-- is music": no watched/unwatched state, no history, no continue-watching.
-- Rename it so the column says what it governs.
ALTER TABLE playlist_settings RENAME COLUMN audio_only TO music;
