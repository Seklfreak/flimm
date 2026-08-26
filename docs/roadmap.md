# Roadmap

## Done

- **Pinned playlists** (2026-08-26) — pin a playlist to the sidebar from the
  playlist page or its card. Pins are Archive's own per-user state, so they
  follow the account to every client. See [api.md](api.md#pinned-playlists).
- **Seeded shuffle** (2026-08-26) — Shuffle starts a real shuffled run that
  previous/next and autoplay follow, and reshuffles on every press. The seed
  lives in the URL, so there is no server-side queue to invalidate.
- **Playlist navigation** (2026-08-26) — previous/next buttons and `n`/`p`.
- **Minimum play time** (2026-08-26) — a video enters history only after
  `MIN_PLAY_SECONDS` of playback, so opening one by accident leaves no trace.
- **Chapters** (2026-08-26) — chapter markers on the scrubber, a chapter list
  under the video and `[`/`]` jumping. TubeArchivist stores no chapters, so
  they come from the mp4 itself (yt-dlp embeds YouTube's chapters at download
  time) with description timestamps as a fallback. See
  [api.md](api.md#chapters).
- **SponsorBlock on the timeline** (2026-08-26) — segments are tinted on the
  scrubber with the category on hover. Per-category skip settings and a manual
  skip button are still open (see Ideas).

## Next

- **Native Apple apps** — iOS, iPadOS and tvOS in SwiftUI, sharing one Swift
  package (API client, models, playback state) and talking to the same
  `/api/v1` backend. Server URL is the only setup; OIDC settings come from
  `GET /api/v1/config`. See [design.md](design.md#platforms) for the
  per-platform layout.

## Ideas

- **SponsorBlock UI** — per-category skip settings and a manual skip button.
  Segments already render on the timeline.
- **Comments** — render TubeArchivist's archived comments under the video.
- **Download queue management** — view and manage TubeArchivist's download
  queue and subscriptions from Archive (add URLs, retry, ignore).
- **Multi-instance** — one Archive backend fronting several TubeArchivist
  instances, with feeds spanning them.
