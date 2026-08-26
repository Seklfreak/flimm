-- Archive keeps only what TubeArchivist cannot hold: users, feeds, per-user
-- watch history and preferences. Videos/channels/playlists live in TA and are
-- referenced by their TA (YouTube) ids as text.

-- A user is keyed on their OIDC `sub` (upserted from the JWT on each request).
CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    oidc_sub     text NOT NULL UNIQUE,
    email        text,
    name         text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

-- A feed is a user-defined group of channels with listing options. The
-- built-in "everything" feed is virtual (options live in user_prefs).
CREATE TABLE feeds (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           text NOT NULL,
    sort           text NOT NULL DEFAULT 'newest',
    hide_seen      boolean NOT NULL DEFAULT true,
    include_shorts boolean NOT NULL DEFAULT false,
    subtitles_only boolean NOT NULL DEFAULT false,
    pinned         boolean NOT NULL DEFAULT false,
    position       integer NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT feeds_sort_check CHECK (sort IN ('newest', 'oldest', 'shortest', 'longest'))
);
CREATE INDEX idx_feeds_user_position ON feeds (user_id, position);
-- At most one pinned feed per user (the one the app opens on).
CREATE UNIQUE INDEX idx_feeds_one_pinned ON feeds (user_id) WHERE pinned;

CREATE TABLE feed_channels (
    feed_id    uuid NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    channel_id text NOT NULL,
    position   integer NOT NULL DEFAULT 0,
    PRIMARY KEY (feed_id, channel_id)
);
CREATE INDEX idx_feed_channels_channel ON feed_channels (channel_id);

-- One row per (user, video): resume position, completion, and the history
-- entry. title/channel_* are snapshots so history search works without TA and
-- entries survive a video being deleted from TA.
CREATE TABLE watch_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video_id        text NOT NULL,
    channel_id      text NOT NULL DEFAULT '',
    channel_name    text NOT NULL DEFAULT '',
    title           text NOT NULL DEFAULT '',
    first_played_at timestamptz NOT NULL DEFAULT now(),
    last_played_at  timestamptz NOT NULL DEFAULT now(),
    position        double precision NOT NULL DEFAULT 0,
    duration        integer NOT NULL DEFAULT 0,
    completed_at    timestamptz,
    hidden          boolean NOT NULL DEFAULT false,
    UNIQUE (user_id, video_id)
);
-- History listing: newest first, hidden rows excluded.
CREATE INDEX idx_watch_events_history ON watch_events (user_id, last_played_at DESC) WHERE NOT hidden;
-- "Continue watching": in-progress rows only.
CREATE INDEX idx_watch_events_in_progress ON watch_events (user_id, last_played_at DESC)
    WHERE NOT hidden AND completed_at IS NULL AND position > 0;

CREATE TABLE user_prefs (
    user_id    uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    prefs      jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
