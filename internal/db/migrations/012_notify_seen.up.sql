-- What a user's notifying feeds have already seen, by video id.
--
-- TubeArchivist's date_downloaded is not "when the file arrived": its
-- indexer writes the same timestamp into date_downloaded and
-- vid_last_refresh, so every metadata refresh of an old video makes it read
-- as downloaded just now. The only durable fact is the id — a video the feed
-- has seen before is not news, whatever its timestamps say. User-scoped like
-- series_seen, so a video in two feeds is announced once.
CREATE TABLE notify_seen (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video_id   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);

-- A feed switched on (or whose sources changed) is seeded before it announces
-- anything: every video its sources hold today is marked seen, so only what
-- arrives afterwards is news. Done by the notifier, not the request, because
-- it walks every page of every source.
ALTER TABLE feeds ADD COLUMN notify_seeded boolean NOT NULL DEFAULT false;
