# Apple apps — implementation plan

A plan for building the iOS, iPadOS and tvOS clients. Written as a handoff: it
assumes no knowledge of the conversation that produced the backend, only what
is in this repository.

Nothing here is built yet. The web client and the whole API are, and the API
was shaped with these apps in mind — every stateful feature already lives on
the server precisely so a native client is thin.

## Read first

1. **[api.md](api.md)** — the contract. It is normative: the web client is
   written against it, and so should these be. Pay particular attention to
   *Authentication*, *Derived media*, *Nav*, *Shuffle*, *Chapters* and
   *Minimum play time*.
2. **[design.md](design.md)** — the product model (feeds, "Everything",
   resume, history, search over subtitles).
3. **`CLAUDE.md`** at the repo root — conventions, and the rule that this
   repository stays generic and publishable. Deployment specifics belong
   outside it. That rule applies to these apps too: no hostnames, no bundled
   server URL, no personal data.

The original design canvas has artboards for all three platforms (iPhone,
iPad, Apple TV). It lives outside the repo; ask for the link if the layouts
are not obvious from the web client.

## The shape

```
┌───────────────┬───────────────┬───────────────┐
│   iOS app     │  iPadOS app   │   tvOS app    │   thin SwiftUI targets
└───────┬───────┴───────┬───────┴───────┬───────┘
        └───────────────┼───────────────┘
                ┌───────▼────────┐
                │ ArchiveKit     │   one Swift package:
                │ (Swift package)│   models, API client, auth, playback state
                └───────┬────────┘
                        │  HTTPS, /api/v1 + /media
                ┌───────▼────────┐
                │ Archive backend│
                └───────┬────────┘
                        │
                 TubeArchivist
```

One package, three targets. The apps differ in navigation and input, not in
logic: iPhone is a tab bar, iPad a `NavigationSplitView` sidebar, tvOS a top
tab bar driven by the focus engine. Anything that decides *what* to show —
feed composition, resume, watch state, chapters, shuffle order — is already
decided by the server. Resist reimplementing any of it locally; that is how
the clients drift apart.

**Talk only to the Archive backend.** Never to TubeArchivist directly. TA sits
behind an auth proxy in typical deployments and native clients cannot complete
that flow — this is the reason the backend exists.

## Setup

Follow the established project conventions for a new app of this kind
(xcodegen `project.yml` rather than a checked-in `.xcodeproj`, secrets in a
gitignored xcconfig, Sentry initialised only in release builds, the standard CI
workflows). Do not invent a different structure.

Add the targets to *this* repository under `apple/`, so the API contract and
its clients change together — the same reason the web client lives here. CI
must keep the existing Go and web jobs green; add a separate macOS job for the
Apple build rather than extending the current one.

## Authentication

The backend uses OIDC (Authorization Code + PKCE) and tells the client how to
reach the provider, so the **server URL is the only thing a user enters**.

1. `GET /api/v1/config` — unauthenticated. Returns `app_name`, `oidc_issuer`,
   `oidc_client_id`, `version`. Use it to validate the URL the user typed *and*
   to configure the auth flow. A friendly failure here is most of the setup UX.
2. Sign in with `ASWebAuthenticationSession` against the issuer. The provider
   must allow a native redirect URI for the app's scheme — that is a
   configuration step on the deployment side, so surface the exact URI the app
   expects in an error message when it fails.
3. Store tokens in the Keychain. Refresh tokens require the provider to grant
   `offline_access`; without it the app will silently log out.
4. Send `Authorization: Bearer <token>` on every `/api/v1` request.

**Media auth is the part that catches people.** `/media/*` accepts a Bearer
header *or* a signed cookie, because browsers cannot set headers on a
`<video>` source. Native clients should use the header via
`AVURLAssetHTTPHeaderFieldsKey` and ignore the cookie path entirely. Verify
early that byte-range requests still carry the header — this is the single
most likely thing to waste a day.

> A simulator caveat worth knowing: an unsigned simulator build cannot use the
> Keychain, and auth SDKs report a missing session after an apparently
> successful sign-in. Sign the simulator build.

## What to build, and what backs it

Everything below already exists server-side. This is a checklist, not a design
exercise.

| Feature | Endpoints |
|---|---|
| Feeds, unseen counts, pinned feed as the launch screen | `GET /feeds`, `GET /feeds/{id}/videos` |
| Feed editor (channels, sort, hide seen, shorts, subtitles-only, pin) | `POST/PUT/DELETE /feeds*` |
| Channels directory, channel page, feed membership | `GET /channels*`, `PUT /channels/{id}/feeds` |
| Playlists, pinned playlists in the sidebar | `GET /playlists`, `/playlists/pinned`, `PUT /playlists/{id}/pinned` |
| History grouped by day, remove an entry | `GET /history`, `DELETE /history/{id}` |
| Search incl. subtitle hits that seek to a timestamp | `GET /search` |
| Preferences (autoplay, speed, subtitle language and size, theme) | `GET /me`, `PATCH /me/prefs` |

### Playback — the details that matter

- **Resume is the default action.** Any saved `position` on an unwatched video
  resumes; there is no threshold to reimplement and no `t=` parameter needed.
  Show where it resumed from and offer "start over"
  (`DELETE /videos/{id}/progress`).
- **Heartbeat** `POST /videos/{id}/progress` every ~10s while playing, and on
  pause, seek, background and termination. The server decides what counts as
  watched (≥90% or ≤30s remaining) and what is too brief to record at all
  (`MIN_PLAY_SECONDS`) — do not duplicate either rule.
- **Context travels with playback.** `feed` / `playlist` / `channel`, plus
  `shuffle=<seed>` and `audio=1`, are the player's state. `GET /videos/{id}/nav`
  returns the neighbours in that context; `up-next` returns what follows. Keep
  the context intact when moving between videos, exactly as the web client
  does with URL parameters — that is what makes previous/next, autoplay and a
  shuffled run agree.
- **Shuffle** is a seed, not a queue. A new seed reshuffles; the same seed
  always produces the same order. Start a run from the `first` field of `nav`.
- **Chapters** (`GET /videos/{id}/chapters`) drive scrubber markers and a
  chapter list; roughly a third of videos have none, so every chapter affordance
  must vanish cleanly when the list is empty. tvOS should map them to the
  chapter controls in the transport bar.
- **Subtitles** come as WebVTT tracks on the video detail; English is the
  default when a track exists. SponsorBlock segments are on the detail too —
  the web client tints them on the scrubber and skips them when enabled.
- **Audio-only** for music playlists: use `audio_url` instead of `media_url`.
  Wire it to `MPNowPlayingInfoCenter` / `MPRemoteCommandCenter` and enable
  background audio; this is where the mode earns its keep on a phone.

## Known complications

**Codecs are the big unknown, and worth settling before writing UI.** The
archive holds whatever was downloaded, which is often modern web codecs, while
AVFoundation supports a narrower set than a browser does. Before committing to
`AVPlayer`, take a handful of real files and check what actually plays on a
device (not just the simulator) and on the tvOS hardware being targeted. There
are three outcomes:

1. It all plays — proceed, and this section is moot.
2. Some plays — decide per video, using the stream metadata, and fall back.
3. Little plays — the apps need a compatible rendition.

The backend is already built for outcome 3. `internal/media` is a **derived
media cache keyed by `(video, variant)`**, not an audio-only feature: it
derives a rendition on first request, caches it on disk, serves it with full
range support, collapses concurrent requests onto one job and evicts
least-recently-used. Audio is simply the first variant. Adding a compatible
video rendition means adding a variant and an endpoint beside
`GET /media/audio/{id}.webm` — not new infrastructure.

Be honest about the cost before choosing it: audio is a stream *copy* and
nearly free, whereas a video rendition is a real transcode. It needs a
deliberate decision about when it runs (on demand, with the first viewer
waiting, versus ahead of time), how much disk it may use, and what the client
shows while it is happening.

**Network reach.** A deployment may deliberately keep the server off the public
internet, in which case the apps only work on the local network or over a VPN.
Do not design an onboarding flow that assumes reachability from anywhere; do
make the "cannot reach server" state clear and non-destructive, and never wedge
a signed-in user because one request failed.

**Offline and downloads** are not in the API today. If offline playback is
wanted, design it as an explicit feature — it interacts with the derived-media
cache, watch-state syncing and storage limits, and should not be smuggled in as
a side effect of caching.

## Suggested order

1. `ArchiveKit`: models, API client, auth, keychain. Test it against a real
   deployment before any UI exists.
2. **Settle the codec question** on a real device.
3. iOS: feeds → player → history. The player is the risk; do it early.
4. iPad: the same views under `NavigationSplitView`; mostly layout.
5. tvOS: focus-driven grids and the transport bar. Budget real time here — the
   focus engine is not a free reflow of the iOS layout.
6. Audio-only, chapters, SponsorBlock, search-to-timestamp.

## Open decisions

- Whether all three ship together or iPhone leads.
- Whether the derived-media cache gains a video variant (see above), and
  whether it is derived on demand or ahead of time.
- Offline downloads: in scope or explicitly not.
- Whether tvOS gets its own simplified feed editor or defers editing to the
  other platforms.
