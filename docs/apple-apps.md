# Apple apps — implementation plan

A plan for building the iOS, iPadOS and tvOS clients. Written as a handoff: it
assumes no knowledge of the conversation that produced the backend, only what
is in this repository.

The web client and the whole API are already built, and the API was shaped
with these apps in mind — every stateful feature lives on the server precisely
so a native client is thin.

## Status

**Phase 1 (skeleton + `FlimmKit`), Phase 2 (the iPhone app), Phase 3 (the iPad
layout) and Phase 4 (the tvOS app) are done.** See
[`apple/README.md`](../apple/README.md) for how to generate the project, run
the apps and run the tests.

What is there:

- `apple/project.yml` — xcodegen input for two targets. `Flimm` is the
  iOS/iPadOS app (iOS 17, `dev.winktech.flimm`, background audio, the
  `dev.winktech.flimm://auth` redirect scheme); `FlimmTV` is the Apple TV app
  (tvOS 17, `dev.winktech.flimm.tv`, background audio, a layered
  App Icon & Top Shelf brand-assets catalogue, and **no** URL scheme, because
  the device grant never redirects back). Both take signing and Sentry from
  the gitignored `Config/Secrets.xcconfig`.
- `apple/Shared` — the app code that is genuinely platform-neutral and is
  compiled into both targets: `AppModel`, `Pager`/`PagerStore`, the `Fmt`
  formatting helpers, the palette and the authenticated `MediaImage`. It is
  the *only* place iOS and tvOS share view-layer code; everything else is
  written twice because a 10-foot focus-driven interface is not the phone's
  layout scaled up. Anything not UI at all belongs in `FlimmKit` instead.
- `apple/FlimmKit` — the shared package, with no UI dependency:
  **models** for every object in [api.md](api.md), an **`APIClient` actor**
  covering every endpoint (bearer injection, one refresh-and-retry on 401,
  typed errors, `mediaHeaders()` and `assetHTTPHeaderFieldsKey` for
  `AVURLAsset`), **auth** (`ServerProbe`, `OIDCClient` with PKCE,
  Keychain-backed `TokenStore`, `AuthSession`), and **playback**
  (`PlaybackContext`, `ProgressReporter`, `WebVTT` cue parsing,
  `ChapterMath`, `SponsorRules`, `CodecGate`). Tested against fixtures cut
  from api.md. Sign-in is a strategy: `Authenticating` has a
  `BrowserAuthenticator` (Authorization Code + PKCE through
  `ASWebAuthenticationSession`) and a `DeviceCodeAuthenticator` (RFC 8628),
  and `AuthSession` holds one without knowing which.
- `apple/Flimm` — the **iPhone and iPad app** (one target, two shells),
  SwiftUI on iOS 17 with `@Observable` view models:
  - **Onboarding** — server URL → `ServerProbe`, with a separately worded
    state for an invalid address, an unreachable server, a server that is not
    Flimm and a Flimm server with no OIDC provider; then sign-in, which names
    the exact redirect URI verbatim when the provider rejects it. Settings
    covers the account, every field of `Prefs`, feed management, the app
    version, sign out and "change server".
  - **Tabs** — Feeds · Channels · Playlists · History with `.searchable` in
    the header. The pinned feed is the launch screen. Video cards carry the
    resume pill, seen check, duration and progress bar; lists page on the last
    row and pull to refresh.
  - **Editing** — the feed editor (name, channel multi-select, sort, hide
    seen, Shorts, subtitles-only, pin), feed reordering, the channel directory
    and channel page with the "In feeds:" control and mark-seen, playlists with
    the pin and music toggles, and history with swipe-to-delete.
  - **Player** — a custom shell over `AVPlayerLayer` (not `VideoPlayer`, so
    the scrubber is ours): resume with a "Resumed from … · Start over" toast,
    a 10 s heartbeat that flushes on pause, seek, background and dismiss,
    context-carrying previous/next and autoplay, shuffle by new seed starting
    at `nav.first`, chapter ticks plus a chapter list, SponsorBlock tints with
    auto-skip on the `skip_sponsors` preference, self-rendered WebVTT
    subtitles, playback speed, Picture in Picture, landscape full screen, and
    audio-only mode with `MPNowPlayingInfoCenter` / `MPRemoteCommandCenter`.
  - **Search** — sectioned results with subtitle hits that open the player at
    their timestamp.
  - **iPad** — the same screens under a `NavigationSplitView`:
    - **One navigation model, two shells.** `NavigationModel` holds the
      selected section, a `NavigationPath` per section, the chosen feed and its
      view filter; `RootShell` renders it as a `TabView` in compact width and a
      sidebar + detail column in regular. iPad multitasking flips the size
      class mid-flow, which tears one shell down and builds the other, so
      nothing about "where am I" may live in a shell's `@State`. The sidebar is
      the web client's: feeds with unseen counts and the pinned one first, a
      Library section, pinned playlists, and Settings at the bottom. Pinned
      playlists and Settings are pushes onto a section's own stack rather than
      sections of their own, so both shells can express them.
    - **Grids.** Video lists, the playlist list and search's video results lay
      out in a `LazyVGrid` with adaptive columns — three across a full-width
      iPad, fewer as the window narrows, one on a phone. The counts come from
      the container's width, so Split View needs no special case.
    - **Player.** Pushed into the detail column, with the chapter list and up
      next beside the video (about ⅔ / ⅓) instead of under it; full screen
      collapses the sidebar, and landscape full screen and PiP are unchanged.
      The `WatchModel` (and with it the `AVPlayer`, audio session and progress
      heartbeat) belongs to `PlayerCoordinator` rather than to the view, so a
      resize that rebuilds the shell does not interrupt playback.
    - **Hardware keyboard**, matching the web client's map: space/`k`
      play-pause, `j`/`l` and the arrows ±10 s, `n`/`p` previous and next,
      `[`/`]` chapters, `f` full screen, `m` mute, `c` subtitles, `,`/`.`
      speed, `/` to focus search, ⌘F search and ⌘, Settings. Player keys are
      `onKeyPress` on the focused player, so typing in a search field keeps its
      own characters.
    - **Multitasking.** `PagerStore` keys each list by what it queries, so the
      screens a size-class flip rebuilds pick their loaded pages back up
      instead of re-fetching them on every resize.
- `apple/FlimmTV` — the **Apple TV app**, SwiftUI on tvOS 17:
  - **Sign-in is the OIDC device authorization grant.**
    `ASWebAuthenticationSession` does not exist on tvOS and there is no
    browser to fall back to, so the TV asks the provider for a code, shows
    `verification_uri_complete` as a QR code beside the user code in very
    large type, and polls the token endpoint — honouring `interval`,
    `slow_down`, `authorization_pending` and `expired_token`. A provider with
    no `device_authorization_endpoint` is a hard stop, and the screen says
    exactly what to enable rather than failing vaguely. See
    [deploy.md](deploy.md#native-apps).
  - **Top tab bar** — Feeds · Channels · Playlists · History · Search ·
    Settings, driven by the focus engine, each with its own
    `NavigationStack`. No shared navigation model: unlike the iPad there is
    no size class to flip, so there is nothing for one to protect against.
  - **Grids** of `.card`-styled buttons — tvOS's own focus lift, shadow and
    parallax rather than a hand-rolled scale — paging five rows before the
    end, because the focus engine moves a whole row at a time. The pinned
    feed is the launch screen; the feed switcher is a focusable row of chips.
  - **Player** — `AVPlayerViewController` behind a
    `UIViewControllerRepresentable`, full-bleed with the native transport
    bar. The asset carries the bearer header; chapters become an
    `AVNavigationMarkersGroup` on the item, SponsorBlock segments become
    `interstitialTimeRanges` (and are skipped outright on the
    `skip_sponsors` preference), previous/next are mapped onto the remote's
    skip gestures via `skippingBehavior = .skipItem`, autoplay follows
    `up-next`, and shuffle is a new seed starting at `nav.first`. Resume is
    the default; "Start over" and the playback preferences live in a custom
    Info-panel tab. Subtitles are rendered from the WebVTT cues in
    `contentOverlayView` — the tracks are authenticated sidecars an
    `AVPlayerItem` cannot fetch itself. Audio-only plays `audio_aac_url` with
    artwork in the overlay and `MPNowPlayingInfoCenter`.
  - **Read-only feeds.** No feed editor: naming a feed and picking its
    channels wants a keyboard and a long list. The screens say "Edit feeds on
    your phone" where the button would otherwise be, rather than leaving
    someone hunting for it.
- `.github/workflows/apple.yaml` — lint, package tests and unsigned iOS and
  tvOS simulator builds on `macos-26`, path-filtered to `apple/**`.

What is still missing:

- **App Store Connect app records.** `testflight.yaml` archives and uploads
  both apps on every version tag (manual signing with the `Flimm App Store`
  and `Flimm TV App Store` profiles). The upload needs an app record per
  platform in App Store Connect, which the API cannot create; the workflow
  stays dormant until `APP_STORE_CONNECT_KEY_ID` is set.
- **A real archive on real hardware.** The codec path is built and the
  fallback is automatic (see *Known complications*), but nobody has yet
  pointed either app at a real archive on a real device to learn how often the
  gate fires at all — and so how much transcoding a normal evening costs.
- **No UI tests.** The logic worth testing was moved into `FlimmKit`, which
  has them; the views themselves are only covered by the compiler. The iPad
  layout and the whole TV app in particular have been built, linted and run
  on the simulator, but without a server to point them at only onboarding can
  be exercised: the sidebar, the grids, the side-by-side player, and on tvOS
  the focus behaviour, the navigation markers and the interstitials have not
  been seen against real data.
- **A Stage Manager / external display pass.** The layout follows the size
  class and so behaves, but nobody has looked at it in a resizable window or
  on a second screen.

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
                │ FlimmKit       │   one Swift package:
                │ (Swift package)│   models, API client, auth, playback state
                └───────┬────────┘
                        │  HTTPS, /api/v1 + /media
                ┌───────▼────────┐
                │ Flimm backend  │
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

**Talk only to the Flimm backend.** Never to TubeArchivist directly. TA sits
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
2. Sign in against the issuer. **On iPhone and iPad** that is
   `ASWebAuthenticationSession`, and the provider must allow a native redirect
   URI for the app's scheme — a configuration step on the deployment side, so
   surface the exact URI the app expects in an error message when it fails.
   **On Apple TV there is no browser and no `ASWebAuthenticationSession`**, so
   it is the OIDC **device authorization grant** (RFC 8628) instead: the TV
   posts to `device_authorization_endpoint`, shows the user code and a QR code
   for `verification_uri_complete`, and polls the token endpoint honouring
   `interval`, `slow_down`, `authorization_pending` and `expired_token`. There
   is no fallback — a provider that doesn't offer the grant cannot sign in a
   TV, and the app has to say so plainly.
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
- **Audio-only** for music playlists and the codec-gate fallback: use
  `audio_aac_url` instead of `media_url` — `audio_url` is Opus in WebM and
  will not play here, and is never a substitute even when `audio_aac_url` is
  missing. `audio_aac_url` is optional (a field added after api.md's first
  cut), so a server that predates it has no working audio-only path at all;
  say so rather than trying `audio_url` anyway. Wire the working case to
  `MPNowPlayingInfoCenter` / `MPRemoteCommandCenter` and enable background
  audio; this is where the mode earns its keep on a phone.

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

**Outcome 3 is built.** `GET /media/hls/{id}/index.m3u8` serves the video
transcoded to H.264 (High@4.1, ≤1080p) with AAC audio, as HLS with fMP4
segments — which is exactly what `AVPlayer` is happiest with. The video detail
carries `hls_url` (always) and `hls_state`
(`pending|running|done|failed`). See
[*Compatible video rendition (HLS)*](api.md#compatible-video-rendition-hls) in
api.md for the contract.

**Both apps do this, and the fallback is automatic.** `FlimmKit`'s
`CodecGate.decision(for:)` returns one of four answers, and the iOS and tvOS
players act on the same one:

1. `.native` — a video stream's codec is playable here (`avc1` always;
   `vp09`/`av01` decided at runtime by `VTIsHardwareDecodeSupported`), so
   `media_url` plays. The original file, no server cost. Also the answer when
   the server reports no `streams` at all: unknown must not read as
   unplayable.
2. `.hls` — nothing decodes here, but the server offers `hls_url`. The player
   posts `POST /videos/{id}/hls` first so the transcode starts before
   AVFoundation opens the playlist, then loads `hls_url` into `AVPlayer` with
   the same `AVURLAssetHTTPHeaderFieldsKey` Bearer header every other media
   route takes. AVFoundation re-sends those headers on **every** request the
   asset makes — the playlist, its re-reads as the EVENT playlist grows, the
   fMP4 init segment and each media segment — which is what lets the whole
   route sit behind the one media gate. That is not folklore:
   `FlimmKitTests/HLSHeaderForwardingTests` builds a real fMP4 HLS stream with
   ffmpeg, serves it from a local socket server that records every request,
   plays it, and asserts the header on all of them. (The test skips itself
   when ffmpeg is not installed.)
3. `.audioOnly` / `.unplayable` — nothing decodes *and* the server predates
   `hls_url`. Only here does the codec wall appear, naming the codec and
   offering audio-only if `audio_aac_url` is there.

What the viewer sees on the `.hls` path is a spinner and **"Preparing a
compatible version…"** over black — on iOS in the player stage, on tvOS in the
`AVPlayerViewController` content overlay — until the item reports
`readyToPlay`. It stays up across the retries, because a playlist whose first
segment does not exist yet answers **503 with `Retry-After: 5`** and `AVPlayer`
has no notion of coming back later: the item simply fails
(`NSURLErrorDomain -1008` wrapping `CoreMediaErrorDomain -16849`, observed on
`AVPlayerItem.status`, *not* through
`failedToPlayToEndTimeNotification`, which never fires for a playlist-level
failure). The player treats that as "still preparing", waits 5 s and reopens
the asset, for up to two minutes without playback before it finally shows the
error. The window rolls forward while the rendition is actually playing, so a
stumble an hour in gets its own two minutes rather than inheriting a spent
one. Where it is cheap to say so — the iOS options menu, the tvOS Info panel —
the UI notes that this is the compatible version, capped at 1080p.

Chapters, SponsorBlock and subtitles are untouched: they are in seconds
against the archived duration, and that duration stays authoritative because a
growing EVENT playlist reports only what has been transcoded so far — adopting
it would shrink the scrubber to a few seconds, so the players do not.

**Seeking is the one thing the rendition genuinely limits.** Until the
transcode has produced that far, seeking past it is *clamped*, not honoured —
including the resume seek, which is exactly the case that matters, since a
half-watched video is the likeliest one to be reopened. Both players re-try an
unlanded resume on every tick until the playlist has grown far enough, and
until it lands they report *the position being sought* as the heartbeat rather
than the player's clock. Without that, resuming an hour-long video at 40
minutes would post "3 minutes" a few seconds later and overwrite a good
position with a worse one. An explicit seek by the viewer replaces the pending
resume, so nobody is dragged back.

Audio-only stays as the cheap fallback and as the music-playlist path:
`audio_aac_url` (`GET /media/audio/{id}.m4a`) is the same audio as AAC in MP4.
`audio_url` is Opus in WebM, which AVFoundation cannot decode — never use it on
Apple platforms. Both are guarded the same way: the app only offers a fallback
when the field is present, and says a newer server is needed otherwise.

Be honest about the cost when the gate fires: the WebM audio variant is a
stream *copy* and nearly free, the AAC one is an audio-only re-encode and
cheap, whereas the video rendition is a real transcode — roughly realtime per
core, ~2–3 GB per 1080p hour on disk, one at a time by default. It is on
demand and progressive (the viewer starts watching while it runs), but it is
not free, which is why the client must only reach for it when the source is
genuinely unplayable.

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

1. ~~`FlimmKit`: models, API client, auth, keychain.~~ **Done.**
2. **Settle the codec question** on a real device — still open in the sense
   that matters. The client handles all three outcomes gracefully, and an
   undecodable video now falls back to the compatible rendition on its own;
   what is unknown is which outcome a real archive actually produces, and so
   how much transcoding it asks of the server.
3. ~~iOS: feeds → player → history.~~ **Done**, including audio-only,
   chapters, SponsorBlock and search-to-timestamp.
4. ~~iPad: the same views under `NavigationSplitView`.~~ **Done** — one
   navigation model behind both shells, adaptive grids, a side-by-side player
   and the web client's keyboard shortcuts.
5. ~~tvOS: focus-driven grids and the transport bar.~~ **Done** — a second
   target over the same package, the device-code sign-in, six focus-driven
   sections and `AVPlayerViewController` carrying Flimm's chapters and
   SponsorBlock natively. The warning held: the focus engine is not a free
   reflow of the iOS layout, and only `apple/Shared` came across unchanged.

## Open decisions

- ~~Whether all three ship together or iPhone leads.~~ iPhone led.
- Whether the derived-media cache gains a video variant (see above), and
  whether it is derived on demand or ahead of time.
- Offline downloads: in scope or explicitly not.
- ~~Whether tvOS gets its own simplified feed editor or defers editing to the
  other platforms.~~ **It defers.** Feeds are read-only on Apple TV and the
  screens say "Edit feeds on your phone" where the control would be. Naming a
  feed and multi-selecting channels needs a keyboard and a long list, both of
  which are bad on a remote and already good on the other three clients.
- ~~How tvOS signs in without `ASWebAuthenticationSession`.~~ **The OIDC
  device authorization grant**, as a second `Authenticating` strategy in
  `FlimmKit`. It is a deployment requirement rather than a fallback: the
  provider must enable the grant for the same client id, and if it doesn't,
  the TV cannot sign in at all.
