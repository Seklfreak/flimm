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
- **The TV's pages are lit, not black.** `TVPageBackground` (in
  `Shared/Palette.swift`) fills a tvOS page with a fall from `pageTop` to
  `pageBottom` and a wide, weak wash of the brand blue off the top-left
  corner. Flat black across a 65-inch panel reads as a hole with menus
  floating in it — there is nothing for a row of cards to sit on — and both
  effects are deliberately too subtle to compete with artwork.
- **Icon art is generated, not drawn.** `scripts/make-icons.py` renders the
  iOS icon, the tvOS layered icon and top-shelf images, and the web favicon
  from one definition of the mark (`brew install librsvg`, then
  `python3 scripts/make-icons.py`). The glyph's size is stated there as a
  fraction of each canvas, which is what keeps it the same weight everywhere;
  edit the shape or the fractions and re-run rather than exporting PNGs by
  hand.
- `apple/Shared` — the app code that is genuinely platform-neutral and is
  compiled into both targets: `AppModel`, `Pager`/`PagerStore`, the `Fmt`
  formatting helpers, the palette, the authenticated `MediaImage` and
  `RenditionSteering`, which keeps the server's transcode pointed where the
  viewer is. It is
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
  `ChapterMath`, `SponsorRules`, `CodecGate` with the quality rule,
  `QualityPreference`/`PlaybackSettings` for the per-device quality choice and
  `DeviceCapabilities` for what this screen and this chip can actually do).
  Tested against fixtures cut from api.md. Sign-in is a strategy: `Authenticating` has a
  `BrowserAuthenticator` (Authorization Code + PKCE through
  `ASWebAuthenticationSession`) and a `DeviceCodeAuthenticator` (RFC 8628),
  and `AuthSession` holds one without knowing which.
- `apple/Flimm` — the **iPhone and iPad app** (one target, two shells),
  SwiftUI on iOS 17 with `@Observable` view models:
  - **Onboarding** — server URL → `ServerProbe`, with a separately worded
    state for an invalid address, an unreachable server, a server that is not
    Flimm and a Flimm server with no OIDC provider; then sign-in, which names
    the exact redirect URI verbatim when the provider rejects it. Settings
    covers the account, every field of `Prefs`, the per-device video quality,
    feed management, the app version, sign out and "change server".
  - **Tabs** — Feeds · Channels · Playlists · History with `.searchable` in
    the header. The pinned feed is the launch screen. Video cards carry the
    resume pill, seen check, duration and progress bar; lists page on the last
    row and pull to refresh.
  - **Editing** — the feed editor (name, channel multi-select, a series picker, sort, hide
    seen, Shorts, subtitles-only, pin), feed reordering, the channel directory
    and channel page with the "In feeds:" control and mark-seen, playlists with
    the pin and music toggles, and history with swipe-to-delete.
  - **Player** — a custom shell over `AVPlayerLayer` (not `VideoPlayer`, so
    the scrubber is ours): resume with a "Resumed from … · Start over" toast,
    a 10 s heartbeat that flushes on pause, seek, background and dismiss,
    context-carrying previous/next and autoplay, shuffle by new seed starting
    at `nav.first`, chapter ticks plus a chapter list, SponsorBlock tints with
    auto-skip on the `skip_sponsors` preference, loudness normalisation applied
    to `AVPlayer.volume` (``LoudnessNormalizer`` in FlimmKit, which the TV
    drives identically), self-rendered WebVTT subtitles, scrub-preview stills above the thumb while dragging (the sheet
    and track from `preview_url`, cropped with `CGImage.cropping`; the parsing
    is `ScrubPreview` in FlimmKit, shared with nothing yet and identical to the
    web's), playback speed, Picture in Picture, landscape full screen, and
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
    `AVNavigationMarkersGroup` on the item, SponsorBlock range segments become
    `interstitialTimeRanges` (and are skipped outright on the
    `skip_sponsors` preference), autoplay follows `up-next` — but never into
    a page marked `suggestions`, which is what the end of a list answers —
    and shuffle is a new seed starting at `nav.first`. When an ending is *not*
    taken by autoplay (``PlaybackEnd``), the overlay says "Finished" and names
    what is up next — a statement, not a menu: a focusable card in
    `contentOverlayView` would take focus from the transport bar that already
    holds previous/next. The phone's card carries its own Replay and Up-next
    buttons, because those controls are Flimm's there. **Skipping stays AVKit's**
    (`skippingBehavior = .default`): left/right moves inside the video and the
    transport bar scrubs. Previous/next are *buttons* —
    `transportBarCustomMenuItems`, plus the same pair in the Info panel — and
    a direction the list cannot go is left out rather than shown dead. They
    were briefly mapped onto the remote's skip gestures (`.skipItem`), which
    silently cost the viewer the scrubber: clicking left or right jumped a
    whole video instead of moving inside this one. Resume is the default;
    "Start over", the quality picker and the playback preferences live in a
    custom Info-panel tab. Subtitles are rendered from the WebVTT cues in
    `contentOverlayView` — the tracks are authenticated sidecars an
    `AVPlayerItem` cannot fetch itself. Audio-only plays `audio_aac_url` with
    artwork in the overlay and `MPNowPlayingInfoCenter`.
  - **Focus and selection are two different things on a TV.** The feed picker
    marks which feed you are looking at with an accent capsule — and that
    capsule is painted inside the button's label, so the fill wraps the words
    rather than the button's frame. Which left `.borderless` with nothing to
    repaint on focus, and moving the remote along the row showed nothing at
    all: you could not see which chip you were about to press, only which feed
    was pinned. The chip now draws both — white and lifted for focus, accent
    for the current feed, and an accent ring when it is both. `FLIMM_OPEN_FEED`
    and `FLIMM_FOCUS_FEED` (Debug only) put the app on a given feed and the
    remote on a given chip, because focus is the half of that row a simulator
    screenshot cannot otherwise reach.
  - **A resumed video must never wait on the segments before it.** The
    compatible rendition is encoded from the resume point first, leaving the
    part before it for a later run — and `AVPlayer` asks for segments *around*
    where it was told to start, including a little before it, and cancels any
    request that has not answered in about four seconds. The job used to treat
    "behind the encoder" as "the encoder is heading there", so those requests
    waited for the whole rest of the video to encode and then wrap around,
    while the player retried every four seconds and showed nothing. A request
    for an unproduced segment behind the run now re-aims it (see
    `HLSJob.Request`). It is the difference between a black screen for minutes
    and picture in under a second.
  - **Stats are a screen on the phone and iPad, and four numbers on the TV.**
    The charts want a pointer: a bar you can hover to read, or tap to see what
    it counts. A remote has neither, so the TV shows the headlines in Settings
    — watched, started, finished, finish rate, top three channels — and says
    the charts are on the phone, iPad or web. It is the same `GET /stats` on
    every platform, including the timezone that decides which evening a
    late-night play belongs to.
  - **Comments are a second Info-panel tab on the TV, and a section under the
    description on the phone and iPad.** Selecting a video on tvOS plays it, so
    there is no detail screen to hang comments on, and the panel AVKit gives a
    custom tab is a wide, short band — a vertical list there shows two
    comments and clips the third. So the TV tab is a *horizontal* row of
    cards, which is the shape that band has and what a remote moves through
    well; the phone and iPad get an ordinary section, **open from the start and
    directly under the description**, because that is where what is said about
    a video belongs. On the iPad it sits in the column with the video rather
    than the one with "up next", for the same reason. Closing it is remembered
    for the rest of the launch, so someone who does not want comments closes
    them once. Both platforms drive one ``CommentsStore`` in `Shared/`; the TV
    tab still loads when it is opened, because a tab nobody moves to is a
    request nobody needs.

    **Replies are a level down, not an expansion.** A band that shows three
    cards cannot also show a thread underneath one of them, so selecting a
    comment replaces the row with that thread — back button, the parent in
    full (the list truncates it to four lines, so this is also how a long
    comment gets read), then the replies. Menu goes back to the row rather
    than closing the panel, and only while a thread is open; otherwise Menu
    stays AVKit's. `FLIMM_OPEN_COMMENT=<n>` (Debug only) opens the nth thread
    at launch, because selecting a card needs a remote a simulator does not
    have.
  - **The Home screen's top shelf shows the pinned feed.** When Flimm is
    focused in the Home screen's top row, tvOS draws a row of what is waiting
    in the feed the app opens on — titles, artwork, and the resume bar on
    anything part-watched — and selecting one opens the app straight into
    playback.

    tvOS draws that row **in its own process**, through a small extension
    (`FlimmTopShelf`) that gets a few seconds, no session of ours, and fetches
    artwork itself with none of our headers. So the extension fetches nothing:
    the *app* writes a snapshot into the App Group's **defaults** and the
    thumbnails into its **container**, and the extension reads it
    (`TopShelfSnapshot`, `TopShelfWriter`, `TopShelfRefresh`). The alternative
    — sharing the keychain so the extension could call the API — puts two
    processes on one OIDC session, and a rotated refresh token consumed by the
    extension is a signed-out app. The cost is that the shelf is as fresh as
    the last time someone opened Flimm, which is also when it changes.

    **An App Group on tvOS is shared *preferences*, not a shared directory.**
    `containerURL(forSecurityApplicationGroupIdentifier:)` returns a path there,
    and writing to it fails: "You don't have permission to save the file". The
    simulator does not enforce that, which is how a version that downloaded
    thumbnails into the container passed every check here and did nothing on a
    real Apple TV. So the snapshot lives in the group's defaults — a few hundred
    bytes — and the artwork is not a file at all: each entry carries an absolute
    `/media/thumb/...` URL with a `media_token` in it, which the system fetches
    itself. The token lasts twelve hours and the app republishes at every
    launch, so an expired one costs the pictures, never the row.

    **It publishes at launch, not only from the Feeds screen.** Publishing what
    that screen already shows costs nothing, but it only runs when that screen
    runs and only when it has something on it — so opening the app on another
    tab, or onto a pinned feed with nothing unseen left, left the row empty for
    ever. A feed with nothing unseen falls back to everything in it, because
    the row exists to offer something.

    Two consequences worth knowing. Signing out **clears** the shelf, because
    it lives outside the app where a signed-out person can still see it. And
    the shelf's actions are URLs — the only channel an extension has back to
    the app — which is why the TV target has a `CFBundleURLTypes` at all
    (`dev.winktech.flimm.tv://play/<id>`, built and parsed by `TopShelfLink`);
    it is not a sign-in redirect, which tvOS has no way to receive.

    **It needs provisioning that the other targets do not**: the App Group on
    both the app and the extension, the extension's own bundle id
    (`dev.winktech.flimm.tv.topshelf`) and its own App Store profile. An App
    Group cannot be created through the App Store Connect API — `/v1/appGroups`
    is not a resource — so that step is done in the developer portal by hand,
    and the app's own profile has to be regenerated afterwards to carry the
    new entitlement. CI installs both profiles (`APP_STORE_TV_PROFILE` and
    `APP_STORE_TV_TOPSHELF_PROFILE`) and names both bundle ids in
    ExportOptions; an archive whose embedded bundles are not all named there
    fails to export.

    **With nothing to show it shows nothing**, answering `nil` so tvOS falls
    back to the app's own top shelf image — which is what belongs there, and
    better than a card explaining itself on someone's Home screen. Diagnosis
    lives in Settings instead: a "Top shelf" row reading "8 from Making ·
    today", "nothing published yet · would publish Making", or "unavailable —
    no app group", and a button that publishes on demand and says which step
    failed. That is enough to tell the three cases apart — the app cannot
    write, the app has not written, or tvOS is not asking because Flimm is not
    in the Home screen's top row — without attaching a Mac to read the
    extension's log.

    The phone and iPad have no equivalent and want none: iOS has no top shelf,
    and a widget is a different feature with a different design.
  - **Scrub previews are the TV's one stated gap.** The web, phone and iPad
    draw `preview_url`'s stills above the scrubber; the TV cannot be handed
    them, because the scrubber there is `AVPlayerViewController`'s and AVKit
    exposes no way to supply trick-play images — it generates its own from the
    asset, which it does for the archived file and not for an HLS rendition
    without an I-frame playlist. Nothing is derived for the TV, so a viewer who
    only ever watches there never pays for a sheet.
  - **"Not interested" is one action in one place.** `apple/Shared/DismissAction.swift`
    holds both the round trip and the context-menu row, because `.contextMenu`
    is the same SwiftUI API on iOS and tvOS — only the gesture that opens it
    differs, and that is the platform's job. What the *list* does with the
    result is the caller's: a feed drops the card and offers an undo (a feed
    never shows a dismissed video), while a channel, playlist, search or
    history list keeps it and flips it to "Not in feeds" with an "Add back"
    entry in the same menu.
  - **An edit form fills itself once.** `.task` and `.onAppear` run again
    every time a screen reappears, and in a `NavigationStack` that includes
    coming back from a pushed child. A form that refills from the server there
    silently throws away what the viewer just did in that child — which is how
    picking channels for a feed, going back and pressing Save saved the old
    list (`FeedEditorView.load()` now guards on `hasLoaded`).
  - **A glyph is not a tap target.** An `Image` inside a `Button` is only as
    tappable as the icon is drawn, which left the phone/iPad transport
    controls at 17–26pt against Apple's 44pt minimum — and worst on iPad,
    where the surface is bigger and usually held at arm's length. Every
    control goes through `.playerHitTarget(_:)` (`PlayerControls.swift`),
    which is `frame(minWidth:minHeight:)` plus the `contentShape` that makes
    the padding around the glyph hit-testable: 44pt compact, 52pt regular,
    with the icons themselves stepping up on iPad too. The scrubber keeps its
    22pt bar but is dragged over 44 — the touch area is padded out and the
    layout pulled back by the same amount, so nothing moves.
  - **A finished video invalidates the lists behind the player.** Marking a
    video seen — or playback reaching the end, which reports `watched` through
    the heartbeat — goes through `AppModel.videoWatchedStateChanged()`, which
    drops the cached pagers. Dismissing the player is not a context change, so
    a list screen's `.task(id:)` never reruns and its `.onAppear` does not
    fire; every list screen therefore carries
    `.reloadsWhenPlayerCloses(request:isStale:reload:)`, which reloads only
    when the cache actually dropped *that* screen's pager. Without it an
    "Unseen" feed or channel keeps listing the video the viewer just watched.
  - **Layout rules the TV enforces.** A segmented picker is sized to its
    labels (`.fixedSize()`), never to a guessed `maxWidth` — a cap truncates
    them ("Uns…", "Co…") as soon as a label or a locale is longer than the
    guess — and any row mixing a picker with buttons keeps its natural width,
    or the picker takes what it wants and squeezes the buttons into a column
    of single letters. A card's meta line carries the channel and the date
    only; subtitles are a **CC badge** on the thumbnail, because a third part
    pushed the date out of a one-line label. Explanatory paragraphs are held
    to ~1100pt: a tvOS row is nearly 1800pt wide, which is a 200-character
    line to read from a sofa. Section headers carry bottom padding, because a
    focused row grows and would otherwise sit on top of the header above it.
  - **The custom Info-panel tab must fit the band it is given, and fill it.**
    The panel is a short strip across the top of the screen, not a screen of
    its own. A tall stack scrolled inside it shows a handful of rows clipped
    mid-height, so the tab is laid out as two columns that fit without
    scrolling — actions on the left, preferences on the right, one line of
    context above — and anything that cannot be made to fit stays out (which
    is why "Up next" is not there: autoplay already decides what follows).
    AVKit also gives the tab no background *and* sizes it to its content, so
    it both paints an opaque ground and is pinned to the panel's width;
    without the pin the ground stops short and the video shows beside it.
  - **Read-only feeds.** No feed editor: naming a feed and picking its
    channels (or series) wants a keyboard and a long list. The admin
    "index this channel's series" control is also absent here for the same
    reason — it lives on the web and iPhone/iPad channel pages. New-series
    announcements are the same kind of decision (subscribe or dismiss), so
    they too stay on the web and phone; the TV just shows the feed's videos. The screens say "Edit feeds on
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

## Analytics

Both apps report anonymous usage to a self-hosted Umami instance through
`FlimmKit`'s `Analytics`, which speaks Umami's `/api/send` directly — no SDK,
no third party in the path — and is the only place a payload is composed, so
the phone, the iPad and the TV cannot drift into three spellings of the same
event.

- **Configuration is baked in**, like the Sentry DSN: `UMAMI_URL` and
  `UMAMI_WEBSITE_ID` in `Config/Secrets.xcconfig` (empty in the committed
  example, so local and CI builds report nothing), passed by
  `testflight.yaml` from repo variables. `Analytics.configure()` runs under
  `#if !DEBUG` only.
- **The server has the last word.** `AuthSession` calls `Analytics.apply(_:)`
  with the server's config on every path that adopts one, and a deployment
  running `ANALYTICS_DISABLED=true` switches reporting off — including
  whatever was already queued.
- **What is sent**: a pageview per `Analytics.Screen` (route patterns —
  `/watch/:id`, never a video id) and four events, `play` (kind, audio-only),
  `search` (scope only, never the query), `feed-created` and `sign-in`
  (`browser` on iOS, `device-code` on tvOS). The visitor id is a random UUID
  in `UserDefaults`, never the account or the token's `sub`; nothing touches
  the IDFA, so neither app needs a tracking prompt.
- The payload's `hostname` is what separates the clients inside one Umami
  website: `flimm.ios`, `flimm.ipados`, `flimm.tvos`. tvOS has no feed editor,
  so it never reports `feed-created` — the one deliberate gap.

## Authentication

The backend uses OIDC (Authorization Code + PKCE) and tells the client how to
reach the provider, so the **server URL is the only thing a user enters**.

0. **A server may have no sign-in at all.** `GET /api/v1/config` says
   `auth_disabled: true` when the deployment runs with `AUTH_DISABLED=true`
   (a local dev server, typically alongside `cmd/fake-ta`). There is no OIDC
   flow to run: the app connects, `AuthSession` opens a session on a static
   token — any non-empty value, because `/media/*` still wants the header even
   though the server ignores what is in it — and Settings says so out loud.
   This is *not* the same as a server that wants auth but publishes no issuer:
   that one is half-configured and stays refused.
1. `GET /api/v1/config` — unauthenticated. Returns `app_name`, `oidc_issuer`,
   `oidc_client_id`, `version`, `auth_disabled`, `analytics_disabled`. Use it to validate the URL the
   user typed *and*
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
   **The refresh token is the session** — there is no cookie behind it — so
   two things follow. The app renews on returning to the foreground
   (`AuthSession.refreshIfNeeded()`), not only when a screen happens to ask
   for data, so the rotation most providers do happens inside a live app and
   the validity window keeps rolling forward for someone who opens the app
   often. And a refresh whose tokens could not be *written* is reported
   (`TokenStore.onPersistFailure`) rather than swallowed: the provider has
   already revoked the token still on disk, so a silent failure here is a
   sign-in screen at the next launch with nothing to explain it. A deployment
   whose provider expires refresh tokens quickly will still sign people out
   after that long away from the app — it is worth setting that lifetime to
   something a TV app can survive.
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
| Feed editor (channels, series, sort, hide seen, shorts, subtitles-only, pin) | `POST/PUT/DELETE /feeds*` |
| Channels directory, channel page, feed membership | `GET /channels*`, `PUT /channels/{id}/feeds` |
| Playlists, pinned playlists in the sidebar | `GET /playlists`, `/playlists/pinned`, `PUT /playlists/{id}/pinned` |
| History grouped by day, remove an entry | `GET /history`, `DELETE /history/{id}` |
| Search incl. subtitle hits that seek to a timestamp | `GET /search` |
| Preferences (autoplay, speed, subtitle language and size, theme) | `GET /me`, `PATCH /me/prefs` |

### Playback — the details that matter

- **Resume is the default action.** Any saved `position` on an unwatched video
  resumes; there is no threshold to reimplement and no `t=` parameter needed.
  Show where it resumed from and offer "start over"
  (`DELETE /videos/{id}/progress`). On the compatible-rendition path the same
  position is also what `POST /videos/{id}/hls?from=` is given **and** what goes
  on the `master.m3u8?from=` URL handed to the player, so the server encodes the
  part being resumed to first and the player starts there via `#EXT-X-START`.
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
  Each segment carries an `action_type`: only `skip` is seeked past, `mute` is
  muted for its length and unmuted afterwards to whatever the viewer had set,
  and `poi`/`full` are never skipped and never tinted. The `poi` is the
  highlight: the phone and iPad draw it as a diamond on the scrubber and put a
  *Jump to the highlight* pill above the transport bar, and the TV — which has
  no scrubber worth hunting on — carries the same jump as an action in the
  Info panel. It is offered only while playback is before it, and regardless
  of `skip_sponsors`. All of that lives in `SponsorRules` and
  `SponsorMuteTracker` in FlimmKit — the phone drives its mute through
  `PlayerEngine`, the TV sets `AVPlayer.isMuted` directly, and neither decides
  anything of its own.
- **Where a caption sits is measured, not assumed** (`SubtitleLift` in
  FlimmKit). Two numbers: with nothing over the video a cue sits a *tenth of
  the picture* above the bottom edge, because a fixed inset that reads as a
  margin on a phone reads as "stuck to the edge" on an iPad; with the controls
  up — which, while paused, is always — it clears the bar by that bar's own
  measured height, because the bar is taller on an iPad, taller again with a
  highlight pill up, and a guessed constant can only be right for one of them.
  The web client applies the same two rules in `cueLineOverChrome`, converted
  into WebVTT line boxes. **On a phone in portrait the picture is too short for
  both**: with the controls up a cue clears the scrubber but shares space with
  the centre transport buttons, which is the geometry, not a bug — landscape
  and full screen have the room and do not.
- **Every tick goes through `PlaybackServices`** (FlimmKit). Three of the
  things a player does per tick are rules rather than platform details — the
  loudness measurement it asks for once, the stall it notices, the SponsorBlock
  decision it applies — and each had been written twice before. Both models now
  hand it what the player knows (time, whether it is stalled, the rendition
  height, the segments, the prefs) and get back what SponsorBlock wants done;
  the gain arrives through a closure, because only the caller knows what a
  player is. Nothing per-tick may be added to `WatchModel` or `TVWatchModel`
  that the other one would also need.
- **A stall is reported, not diagnosed.** `StallReporter` notices the picture
  stopping mid-playback and posts `POST /videos/{id}/stall`; the *server*
  attributes it, because it is the only side that knows whether the segment
  existed yet (see [api.md](api.md#playback-stalls)). Both Apple targets and
  the web client apply the same 0.4 s floor and abandon a stall that was still
  running when playback stopped.
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
AVFoundation supports a narrower set than a browser does. It is a difference of
degree, not of kind: the **web client falls back the same way** — Safari has no
AV1 decoder, and HEVC and VP9 support vary by browser and by machine — so it
runs the identical gate and quality rule in `frontend/src/player/codecGate.ts`,
loading a rendition (through hls.js, or natively in Safari) whenever the
archive will not decode. Treat that as the reference implementation of the rule
rather than as a client that never needs one. Before committing to `AVPlayer`,
take a handful of real files and check what actually plays on a device (not
just the simulator) and on the tvOS hardware being targeted. There are three
outcomes:

1. It all plays — proceed, and this section is moot.
2. Some plays — decide per video, using the stream metadata, and fall back.
3. Little plays — the apps need a compatible rendition.

**Outcome 3 is built.** The compatible rendition serves the video transcoded to
a codec Apple decodes, with AAC audio, as HLS with fMP4 segments — which is
exactly what `AVPlayer` is happiest with. The video detail carries `hls_url`
(always), `hls_state` (`pending|running|done|failed`) and `hls_variants`, the
ladder of qualities. See
[*Compatible video renditions (HLS)*](api.md#compatible-video-renditions-hls) in
api.md for the contract.

`hls_url` and each `hls_variants[].url` now end in `master.m3u8` — a
multivariant (master) playlist that names the codecs so browser hls.js will
schedule the fMP4 fragments. **This needs no change in the apps:** they load
whatever URL `hls_url`/the variant hands them, and `AVPlayer` plays a master
playlist exactly as it played the media playlist before. The media playlist
stays reachable at `index.m3u8` in the same directory (the master points at it),
so the byte-range and header-forwarding paths are unaffected.

**Codecs and the quality picker.** `hls_variants` lists every height the video
offers, tallest first, each with its own `url`, `state` and `codec`: `h264` at
1080 and below, `hevc` at 1440 and 2160. Both decode in hardware on every
device these apps target — HEVC since the iPhone 7 and the first Apple TV 4K —
so `AVPlayer` takes either as it stands; the HEVC tracks are `hvc1`-tagged,
which is the part AVFoundation is strict about. **Both apps ship a picker over
that ladder**, and this is how it behaves:

- **The choice is per device, not per account.** `QualityPreference` (`.auto`
  or `.height(Int)`) lives in `PlaybackSettings`, an `@Observable` backed by
  `UserDefaults` under `videoQuality` — never `PATCH /me/prefs`, because an
  Apple TV on ethernet wants a different answer from a phone on cellular. It
  outlives the video, the app and a sign-out.
- **The Auto rule**, resolved by `CodecGate.decision(for:preference:device:)`
  at play time:
  1. If the archive is natively decodable **and** the preference is `.auto` →
     `.native`: the original file, full quality, nothing for the server to do.
  2. Otherwise pick from `hls_variants`, out of the rungs this device can
     decode at all (`hevc` is dropped when
     `VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC)` is false).
     `.height(h)` takes that height or the **nearest lower** one offered;
     `.auto` takes the **tallest rung at or below the screen's pixel height**
     (`DeviceScreen`: the short side of the active window scene's
     `nativeBounds` — 2160 on a 4K Apple TV, 1080 on an HD one, ~1200 on a
     current phone, so a phone lands on 1080 and a 4K TV on 2160). Either way
     a ladder that starts above what was asked for falls to its smallest rung
     rather than to nothing.
  3. Nothing pickable — an older backend without `hls_variants`, or a ladder
     this device cannot decode at all — falls to the archive if it plays, then
     to `hls_url` as before, then to the codec wall.

  The subtle case is deliberate: a **natively decodable** archive with an
  explicit `.height(720)` plays the 720p rendition, because that is a request
  for less data, not a mistake. Only `.auto` reads "playable" as "best".
- **Start the one you will play, where you will start playing it.**
  `POST /videos/{id}/hls?height=<h>&from=<seconds>` runs before the URL is
  handed to `AVPlayer` — `APIClient.startHLS(_:height:from:)`, with the height
  the gate chose and no other, and with the resume position the server itself
  handed over (0 for "start over"). `from` is what makes resuming instant: the
  encoder produces that part of the video first instead of the forty minutes
  nobody is going to watch. The server transcodes one job at a time, so warming
  the whole ladder would only make the played rung later.
- **Also pass `from` on the URL you hand the player.** The resume position must
  go on the **master/playlist URL** too — `…/master.m3u8?from=<resume position>`
  — not only in the `POST`. That is what adds the `#EXT-X-START` the player
  needs: without it `AVPlayer` fetches segment 0 first to lay out the timeline
  before it honours the seek to the resume point, and segment 0 is the segment
  the resume-first transcode produces **last**, so playback stalls waiting for a
  segment that will not exist for minutes. With `?from=` the player starts at the
  resume point and fetches that segment first — the one produced first, so
  resuming a long video now starts instantly instead of stalling. Both watch
  models do this: the `.hls` branch passes the resume position to
  `client.mediaURL(_:from:)`, which appends `?from=<seconds>` to the master URL
  the `AVURLAsset` is built from. (The same offset still goes on the `POST`; the
  contract is that the URL, not only the `POST`, carries `from`.)
- **A switch is a reload.** Each height is its own independent playlist (a
  single-variant master over its own media playlist), not one master listing
  every height for the player to switch between, so `setVideoQuality(_:)` on both
  watch models remembers the clock, starts the new height's job at that
  position, swaps the `AVPlayerItem` for `…/master.m3u8?from=<currentTime>` and
  seeks back once it is ready — playback carries on where it was. Passing
  `?from=` on the switched-to URL matters for the same reason resume does: the
  new rendition's segment 0 is transcoded last, so the `#EXT-X-START` is what
  makes the switch land at the current position instead of at 0:00. A rung the server has not made yet raises
  the same "Preparing a compatible version…" overlay a fresh video does, with
  the same retry loop behind it.
- **Where the picker is.** On iOS, a *Quality* submenu in the player's options
  menu: `Auto`, then `Source · 2160p · AV1` as a disabled line naming what Auto
  plays when the archive decodes here, then a row per rung
  (`2160p · HEVC · ready`, `1080p · preparing`, `720p`) with a checkmark on the
  current one and that rung's own `state` as the hint — `ready` for `done`,
  `preparing` for `running`, nothing for `pending`, which is the normal state
  of most of the ladder. On tvOS, a *Quality* row in the
  `AVPlayerViewController` Info panel stepping through Auto and the offered
  heights, in the same one-click idiom as Speed and Subtitles, with a line
  under it saying what Auto settled on. Both apps also carry a **Video
  quality** row in Settings — `Auto`, `2160p`, `1440p`, `1080p`, `720p`,
  `480p` — as the default for future playback.
- **`state` is per height.** A video can be `done` at 720 and `pending` at
  1080, and the picker's rows say so instead of assuming one state for the
  video.
- **The un-suffixed URL still works.** An older build hitting
  `/media/hls/{id}/index.m3u8` (or `/master.m3u8`) gets the 1080p rendition. Where a server has no
  `hls_variants` at all the apps still play `hls_url`, but with no height to
  name it by: the picker hides itself and `startHLS` is called without one.

**Both apps do this, and the fallback is automatic.** `FlimmKit`'s
`CodecGate.decision(for:preference:device:)` returns one of four answers, and
the iOS and tvOS players act on the same one:

1. `.native` — a video stream's codec is playable here (`avc1` always;
   `vp09`/`av01` decided at runtime by `VTIsHardwareDecodeSupported`), so
   `media_url` plays. The original file, no server cost. Also the answer when
   the server reports no `streams` at all: unknown must not read as
   unplayable.
2. `.hls` — a rendition plays: either nothing decodes here, or a height was
   picked by hand. The decision carries the chosen `hls_variants` entry (`nil`
   on a server that has only `hls_url`). The player posts
   `POST /videos/{id}/hls?height=` first so that transcode starts before
   AVFoundation opens the playlist, then loads the variant's URL into
   `AVPlayer` with
   the same `AVURLAssetHTTPHeaderFieldsKey` Bearer header every other media
   route takes. AVFoundation re-sends those headers on **every** request the
   asset makes — the playlist, any re-read of it, the fMP4 init segment and
   each media segment — which is what lets the whole route sit behind the one
   media gate. That is not folklore:
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
`readyToPlay`. Where the server has said how far the encoder has got, the
overlay says it too: **"Preparing a compatible version… 37%"**, from
`HLSStatus.progress`, polled with the same idempotent `POST` every 5 s while
the job is `running` and the player has nothing to show. It stops as soon as
the rendition plays; `0%` is never shown, because a job that has reported
nothing reads as stuck.

The overlay also stays up across the retries, because a playlist the server
cannot open — the job failed to start — answers **503 with `Retry-After: 5`**
and `AVPlayer` has no notion of coming back later: the item simply fails
(`NSURLErrorDomain -1008` wrapping `CoreMediaErrorDomain -16849`, observed on
`AVPlayerItem.status`, *not* through
`failedToPlayToEndTimeNotification`, which never fires for a playlist-level
failure). The player treats that as "still preparing", waits 5 s and reopens
the asset, for up to two minutes without playback before it finally shows the
error. The window rolls forward while the rendition is actually playing, so a
stumble an hour in gets its own two minutes rather than inheriting a spent
one. Where it is cheap to say so — the iOS options menu, the tvOS Info panel —
the UI names the rendition that is playing: `1080p · compatible version`.

Chapters, SponsorBlock and subtitles are untouched: they are in seconds
against the archived duration, and the rendition agrees with it — the playlist
is a **complete VOD one from the very first request**, every segment listed and
`#EXT-X-ENDLIST` on the end, so the item's duration is the video's own.

**Seeking is a normal seek.** Because the playlist is complete, `AVPlayer` will
seek anywhere in it whatever the encoder has reached; a segment that does not
exist yet blocks server-side (up to ~60 s) while it is made rather than 404ing,
so the player just buffers and the existing spinner covers it. That is what
resume rides on: the position the server handed over goes out as `from` on
`POST /videos/{id}/hls`, so the encoder starts *there*, and the player issues
**one** seek when the item reports `readyToPlay`, exactly as it does on the
native path. Resuming an hour-long video at 40 minutes plays at 40 minutes.

A seek the viewer makes is steered the same way: `RenditionSteering` (shared by
both players) re-points the encoder with another `from` — debounced by a
second, because dragging a scrubber is dozens of seeks and only where it
settles counts, and skipped when the rendition is already `done` or the target
is where the encoder is pointed anyway. It deliberately does **not** model what
has been produced: the server knows which segments exist and whether the run is
already heading for the one being asked for, and ignores a `from` it does not
need. `progress` — `hls_progress` on the wire, on each `hls_variants` entry and
on the `POST /videos/{id}/hls` response — is the fraction of the whole rendition
that exists. It is a number to count up in the overlay, not one to infer a
produced region from.

None of the old compensations survive: `trustsItemDuration` is gone (there is
one duration now), the per-tick re-seek loop that waited for an EVENT playlist
to grow is gone, and the heartbeat reports the player's clock rather than the
position being sought. The one guard that stays is that an item which has not
reached `readyToPlay` has no clock at all — it reads 0 — so until it does, both
players report the position playback is about to start from, which is the value
the server already holds.

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
