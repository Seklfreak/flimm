# Roadmap

## Done

_Nothing shipped yet._

## Next

- **Native Apple apps** — iOS, iPadOS and tvOS in SwiftUI, sharing one Swift
  package (API client, models, playback state) and talking to the same
  `/api/v1` backend. Server URL is the only setup; OIDC settings come from
  `GET /api/v1/config`. See [design.md](design.md#platforms) for the
  per-platform layout.

## Ideas

- **SponsorBlock UI** — show segments on the timeline, per-category skip
  settings, manual skip button (data is already in the video detail).
- **Comments** — render TubeArchivist's archived comments under the video.
- **Download queue management** — view and manage TubeArchivist's download
  queue and subscriptions from Archive (add URLs, retry, ignore).
- **Multi-instance** — one Archive backend fronting several TubeArchivist
  instances, with feeds spanning them.
