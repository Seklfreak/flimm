-- "Tell me when this feed has something new." A feed can notify: every few
-- minutes the server looks at what TubeArchivist has downloaded for the feed's
-- sources since `notified_at` and pushes one notification per feed to the
-- user's devices. `notified_at` is the high-water mark, set to *now* when the
-- flag is switched on, so a feed with a backlog announces only what arrives
-- after that — the same baseline rule series watches follow.
ALTER TABLE feeds ADD COLUMN notify boolean NOT NULL DEFAULT false;
ALTER TABLE feeds ADD COLUMN notified_at timestamptz;

-- The devices a user's notifications go to: one row per APNs device token.
-- The token identifies the device, so it is the key — a device that signs in
-- as someone else moves to them rather than notifying both. `environment` is
-- which APNs the token belongs to: a build from Xcode registers a sandbox
-- token, a TestFlight or App Store build a production one, and a push sent
-- to the wrong one is refused.
CREATE TABLE push_devices (
    token       text PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    platform    text NOT NULL DEFAULT 'ios',
    environment text NOT NULL DEFAULT 'production',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT push_devices_environment_check CHECK (environment IN ('production', 'sandbox'))
);
CREATE INDEX idx_push_devices_user ON push_devices (user_id);
