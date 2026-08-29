# Roadmap

## Done

- **SponsorBlock, per category** (2026-08-29) — one "skip sponsors" switch
  decided everything, with a hardcoded set of categories that acted and the
  rest tinted and ignored. Each category is now the viewer's own: **Skip**
  jumps it, **Ask** offers a *Skip the intro* button in the player for as long
  as playback is inside it, **Off** leaves it alone. The three that interrupt
  a video without being part of it keep skipping by default; intro, outro,
  recap, filler and the rest now default to Ask, because those are sometimes
  what someone came for. `skip_sponsors` stays as the master switch, the
  settings live in `sponsor_actions` on `/me/prefs`, and the decision is made
  in one place per platform — `chapterMath.ts` and FlimmKit's `SponsorRules`,
  which now take the prefs rather than a category list, so the master switch
  cannot be forgotten at a call site. Settings and the button are on all four
  clients; on the TV the offer is a row in the info panel, beside *Jump to the
  highlight*, because its overlay takes no focus.

- **A cached list that never loaded is no longer served as empty** (2026-08-29)
  — the other half of the cancelled-request fix. A pager whose first load was
  cancelled holds no items and nothing retries it, because every later pass
  finds it in the cache and hands it straight back; before the cancellation
  fix that showed "Something went wrong: cancelled", after it a feed reading
  "All caught up" over four unwatched videos. `PagerStore` now only returns a
  pager that actually loaded — one that loaded and *failed* is still returned,
  because it carries an error the screen offers a retry for.

- **Unseen opens with what you were watching** (2026-08-29) — the videos a
  viewer is part-way through now head the unseen view of every feed, most
  recently played first, and the "In progress" / "Continue" filter is gone
  from all four clients: it listed exactly what is now at the top of the list
  it filtered. Composed on the server, so no client had to learn the order —
  the in-progress head is eager and bounded, the tail is the same lazy walk
  minus what the head showed, and how much of the head a page served rides
  the cursor, so a head longer than one page still pages without repeats.

- **A cancelled request is no longer an error on screen** (2026-08-29) — the
  phone could open on "Something went wrong: cancelled" where a feed should
  be. `URLSession` reports a cancelled task as `URLError.cancelled` rather
  than `CancellationError`, so the pager's cancellation branch never caught
  it, and because the failed pager is cached by key it never retried either.
  `APIError` has a `cancelled` case now, and a cancelled load leaves the
  screen alone.

- **A resumed transcode starts at the resume point** (2026-08-29) — a video
  the device cannot decode is converted on demand, and that conversion was
  supposed to begin where the viewer left off. For any source whose *audio*
  could be copied — which is most of them — it began at zero instead: the
  attempt ladder marked a copy rung as "one pass over the whole video", a rule
  that belongs to a *video* copy (a stream copy can only cut on the source's
  own keyframes), and applied it when only the audio was being copied. The
  effect was a viewer resuming an hour in and waiting for the encoder to walk
  there from the beginning. Found by watching the dev stack rather than
  reading the code: `ffmpeg` had no `-ss` and wrote `seg00000` for a request
  that asked for 4:45.

- **Resume gives back fifteen seconds** (2026-08-29) — coming back to a video
  used to drop the viewer exactly where they stopped, which is usually the
  middle of a sentence. The resume point reported by the API is now 15 seconds
  earlier. It is done where the position is composed rather than in four
  players — resume position is the server's to decide — so the web, phone,
  iPad and TV all got it without a line of client code; what is stored, and
  written back to TubeArchivist, is unchanged, and `progress` is still
  computed from the true position so progress bars do not move.

- **The Apple TV's video settings show the video** (2026-08-29) — the tvOS
  info panel painted itself an opaque black slab with square corners over the
  playing video, which is an odd way to present *quality*, *subtitles* and
  *speed*: the thing being judged was hidden behind the judging. It is now a
  dark blur with rounded corners, inset from the panel edges, so the picture
  stays visible behind the settings that change it.

- **The Apple TV's pages have a ground** (2026-08-28) — every tvOS screen was
  filled with pure black, which at 65 inches reads as a hole that the menus
  float in. Pages now use the web client's `--c-bg` rather than black,
  falling a few percent darker towards the bottom, with a wide, weak wash of
  the brand blue off the top-left corner so the screen looks lit rather than
  switched off. Both are far below what artwork or a video needs to sit on
  top of.

- **Subtitles that sit right and stop shouting** (2026-08-28, placement
  corrected 2026-08-29 — tvOS hands a hosting controller 60pt of overscan
  inset, so padding measured from the safe area lands 60pt higher than it
  reads; the TV's cues now ignore the safe area and are measured from the
  panel edge, 84pt up, 300 while the transport bar is showing) — the Apple TV
  drew its cues 120pt off the bottom whether or not anything was there, which
  read as floating in the middle of the picture; they now sit 60pt up and step
  to 240 only while AVKit's transport bar is showing (its delegate is the only
  notice AVKit gives). The black plate behind the words is gone on all three
  players — web `::cue`, the phone/iPad overlay and the TV's — and put back
  the next day: shadows alone read as thinner over a bright scene, and the
  plate is what the viewer had been reading against. The placement half of
  this stands.

- **Sessions that survive time away** (2026-08-28) — the Apple apps asked for
  a sign-in now and then, usually noticed right after an update because an
  update is what forces the cold launch that asks. Only `invalid_grant` ever
  signs the apps out, so this was the refresh token dying: providers commonly
  expire them after a month, and the apps renewed only when a screen asked for
  data. They now renew on returning to the foreground
  (`AuthSession.refreshIfNeeded()`), which keeps the provider's rotation
  inside a live app and rolls the window forward with ordinary use, and a
  refresh that cannot be written to the Keychain is reported instead of
  swallowed — that case revokes the token on disk and would otherwise look
  like a random logout a launch later.

- **The mark fills its canvas** (2026-08-28) — the app icons carried a glyph
  at about a quarter of the icon, which reads as a small mark adrift in a blue
  square at the size a home screen actually shows it. Every piece of icon art
  is now generated by `scripts/make-icons.py` from one definition — the iOS
  icon, the tvOS layered icon and its top-shelf banners, and the favicon —
  with the glyph stated as a fraction of each canvas (62% of an app icon, 55%
  of the tvOS icon's height, 40% of a top shelf). The web sidebar's wordmark
  grew to match, and its inline mark now uses the glyph's own bounds as its
  viewBox instead of floating in a third of the box.

- **Up next earns its space** (2026-08-28) — two things it was missing.
  *Not interested* now works from the up-next list, on the web and in the
  phone/iPad player's details pane (tvOS has no up-next list — the info panel
  deliberately leaves it out, since autoplay already decides what follows).
  The video leaves the list rather than staying marked, because up next never
  contains a dismissed video, and the slot it vacates carries the undo. The
  web's up-next sidebar also collapses now, with the choice remembered in
  `localStorage` per browser rather than in `/me/prefs`: there is no sidebar
  to collapse on a phone or a TV, so it is not an account preference.

- **The resume chip retires itself** (2026-08-28) — "Resumed from 12:31 ·
  Start over" is an offer, useful while a viewer works out where they are and
  in the way afterwards. It now disappears a minute of playback past the
  resume point on all four clients, the rule living in FlimmKit's
  `ResumeNotice` for the Apple ones and mirrored in the web player. Measured
  in playback rather than wall clock, so a pause to decide does not spend the
  minute.

- **Web subtitles are sized off the player again** (2026-08-28) — cues were
  sized in `em` (`0.55/0.7/0.9em`), which relied on Chrome resolving that
  against its own default cue size, 5% of the video height. Chrome now
  resolves it against the video element's inherited font size — a flat 16px —
  so every setting collapsed to ~11px, the same in a small window and in
  fullscreen. The scale now lives in `frontend/src/player/cueSize.ts`, which
  measures the player box and writes the size in absolute px (the old factors
  multiplied through that 5%, so the settings mean what they always meant),
  with a per-setting floor so the three stay distinct in a small window. The
  Apple clients already drew their own overlay at explicit point sizes and
  needed no change. Web cues also sit two lines above the bottom edge now
  (WebVTT `line: -3`, applied only where the .vtt left placement to the
  browser) rather than against it and under the control bar — the clearance
  the Apple overlays already had.

- **Usage analytics on all four clients** (2026-08-28) — Flimm now reports to
  a self-hosted [Umami](https://umami.is) instance from the web app, the
  iPhone, the iPad and the Apple TV, so it is possible to see which screens
  and features are actually used. One shared vocabulary across the platforms:
  a pageview per screen and four events (`play`, `search`, `feed-created`,
  `sign-in`), composed in exactly one place per platform —
  `frontend/src/lib/analytics.ts` on the web and `FlimmKit`'s `Analytics` on
  Apple, which posts Umami's `/api/send` payload directly rather than
  shipping an SDK. Deliberately incurious: Umami's auto-tracking is off
  because it would report `location.pathname` and `document.title` — the id
  and the title of whatever you are watching — so every payload carries a
  route *pattern* (`/watch/:id`) and no event carries a video, channel,
  playlist, search term or account. The endpoint is baked in at build time
  (`VITE_UMAMI_*` build args for the image, `UMAMI_*` xcconfig values for the
  apps), and because this is a public self-hosted product the deployment gets
  the last word: `ANALYTICS_DISABLED=true` publishes `analytics_disabled` on
  `GET /api/v1/config` and every client, App Store builds included, stops
  reporting. See the README's "Analytics" and
  [apple-apps.md](apple-apps.md#analytics).

- **"Not interested": clearing a feed without watching** (2026-08-27) — the
  only way to take something out of a feed used to be *Mark seen*, which lies
  about the watch state and, because Flimm writes watched back to
  TubeArchivist, followed the viewer into TA's own UI and every other client.
  Dismissal is now Flimm's own per-user state in a `dismissed_videos` table
  (deliberately not the `hidden` flag on `watch_events`, which means "removed
  from history" and returns on the next play), with
  `POST`/`DELETE /videos/{id}/dismiss` and a `dismissed` field on every video.
  The **server** drops dismissed videos from every feed — Everything included,
  in every view — and from *up next*, so autoplay never plays one and no
  client has to filter; channel pages, playlists, search and history still
  show them marked, which is where they are put back. All four clients offer
  it: the web card has a control and leaves an undo slot in the grid, the
  phone, iPad and TV share one context-menu row
  (`apple/Shared/DismissAction.swift`) with an undo banner under the list, and
  the History list also gets a swipe. See [api.md](api.md#video-summary) and
  [design.md](design.md).

- **A QA pass over all four clients** (2026-08-27) — the web app, the iPhone,
  the iPad and the Apple TV were each walked end to end against the local dev
  stack, and what came back was mostly **parity gaps** rather than broken
  features. Fixed: the web had **no Settings screen at all**, so `theme` and
  `skip_sponsors` were unreachable there while both other platforms exposed
  them (now `/settings`, in the sidebar and behind a gear on narrow windows,
  with a test that asserts *every* preference the API carries is present); the
  tvOS channel page never showed the channel's **playlists**, which
  `design.md` has always said it should and the phone always did; a finished
  video left **stale "Unseen" lists** behind the player on both the phone and
  the TV, now invalidated through one funnel and reloaded by one shared
  modifier; the iPhone's search filters rendered as **two switches with no
  labels**; the iPad's up-next column broke titles mid-word in portrait with
  the sidebar out; the web `Segmented` control **hid options it could not
  fit** (the feed editor's "Longest" sort was unreachable by mouse); the web
  player showed no loading state on the direct-file path and hid its controls
  over a black frame; and `cmd/fake-ta` was encoding test patterns at 15.7
  Mbit/s, making a 45-second clip 88 MB and the fixture a gigabyte.
  Two reported defects were **refuted with evidence** rather than fixed: tvOS
  cards "overlapping" is the platform's focus parallax (measured: identical
  widths and a normal gap with nothing focused), and a web playback stall
  reproduced with no Flimm code in the path at all — Chrome's own player
  stalls on the same file from a static server, while AVFoundation plays it on
  two platforms.
- **Apple TV display fixes** (2026-08-27) — a walkthrough of the whole tvOS
  app against the local dev stack turned up five real defects, all now fixed:
  the custom Info-panel tab rendered **with no background at all**, drawing its
  preference rows straight onto moving video; every segmented picker truncated
  its labels ("Uns…", "Co…", "From chann…") because each screen capped it with
  a guessed `maxWidth`; a card's meta line lost its date to the ellipsis ("The
  Workshop · CC EN · 5 d…"), so subtitles became a CC badge on the thumbnail
  and the line kept the channel and the date; Settings paragraphs ran the full
  ~1800pt of a tvOS row; and a focused row sat on top of the section header
  above it. Fixing the pickers first introduced a sixth — a picker sized to its
  labels squeezed "Shuffle" and "Mark all seen" into columns of single letters
  — so the rows that mix a picker with buttons now keep their natural width.
  See [apple-apps.md](apple-apps.md).
- **A local dev stack: no auth, no TubeArchivist** (2026-08-27) — the whole
  product now runs on a laptop with nothing behind it. `cmd/fake-ta` stands in
  for TubeArchivist: the subset of its API Flimm calls, over a fixed catalogue
  of 13 videos across 4 channels, with the media generated by ffmpeg on first
  run so playback, seeking, resume, chapters (embedded *and* in the
  description), subtitles, search-to-timestamp and the custom-playlist actions
  all work against it. The picture is a running timer, so a seek can be checked
  by eye; one video is VP9, so the codec gate and the compatible HLS rendition
  are exercised too. Its tests drive the **real `ta.HTTP` client**, which is
  the only way a fake stays honest. Nothing it holds reaches a real archive.
  The native apps can use it because `/api/v1/config` now publishes
  **`auth_disabled`**, and the iPhone, iPad and Apple TV apps connect to such a
  server with no sign-in step — in release builds too, since `AUTH_DISABLED` is
  a deliberate server-side mode the web client already honours and there is no
  credential to protect. A server that *wants* auth but publishes no issuer is
  still refused: those two cases are opposites, which is why the server says
  which one it is rather than leaving it to be inferred. Settings names it
  ("no sign-in; everyone who can reach it shares this account") and offers
  *Disconnect* rather than a sign-out that cannot mean anything. See
  [README](../README.md#running-locally) and [api.md](api.md#authentication).
- **tvOS: the scrubber back, previous/next as buttons** (2026-08-27) — the
  Apple TV player mapped previous/next onto the remote's skip gestures
  (`skippingBehavior = .skipItem`), so clicking left or right jumped a whole
  video and there was no way to move *inside* one: the transport bar's whole
  reason for existing was gone. Skipping is AVKit's again (`.default` — click
  to move ±10s, swipe to scrub), and stepping through the list is a pair of
  buttons in the transport bar (`transportBarCustomMenuItems`) and in the Info
  panel, each left out when the list cannot go that way. See
  [apple-apps.md](apple-apps.md).
- **Jump to the highlight** (2026-08-27) — the `poi` segment the backend
  started fetching is now something every client offers: sponsor, self-promo
  and the rest are what a player *skips*, but a point of interest is where a
  contributor marked "this is where the video actually starts", so it is
  offered and never taken. It shows only while playback is still before it,
  and regardless of `skip_sponsors` — jumping is a click, not a skip. The web
  player and the phone/iPad draw a marker on the timeline and a pill button;
  the Apple TV, which has no scrubber worth hunting on, carries the same jump
  as an Info-panel action. The rule (which point, and when it is worth
  offering) is `highlightToOffer` in `chapterMath.ts` and in FlimmKit's
  `SponsorRules`, not three separate answers. See
  [api.md](api.md#sponsorblock).
- **SponsorBlock fetched first-party** (2026-08-27) — segments no longer come
  from the snapshot TubeArchivist indexed at download time. `internal/sponsorblock`
  asks the service itself (`SPONSORBLOCK_URL`, default `sponsor.ajay.app`) and
  the video detail is served from that, falling back to TA's copy only when
  there is no service configured or the lookup failed; an answer of "no
  segments" is authoritative, so a segment that was removed or downvoted away
  does not come back. The lookup never names the video: it sends the first four
  hex characters of `sha256(video_id)`, takes back every video sharing that
  prefix and filters locally, caching answers for six hours and a failure for
  ten minutes so an offline deploy costs one timeout, not one per request. It
  runs concurrently with the watch-state and channel queries, and segments are
  clamped to *this* cut of the video.
  Because all categories and action types are now asked for, `sponsorblock`
  entries carry **`action_type`** and every client learned it in the same
  change: only `skip` is seeked past, `mute` mutes for the segment's length and
  restores the viewer's own mute setting afterwards (web, iPhone/iPad and
  Apple TV), and `poi`/`full` are never skipped and never tinted. The rule
  lives in one place per platform — `chapterMath.ts` and FlimmKit's
  `SponsorRules`/`SponsorMuteTracker`. `GET /videos/{id}/chapters` gained a
  third source, **`sponsorblock`**: crowd-sourced chapter names, used when the
  file embeds none, ahead of the description heuristic. See
  [api.md](api.md#sponsorblock) and [deploy.md](deploy.md#outbound-network-sponsorblock).
- **Instant resume via `#EXT-X-START`** (2026-08-27) — `?from=<seconds>` on the
  playlist/master URL now also moves the *player's* start position to the resume
  point: the media playlist gains an `#EXT-X-START:TIME-OFFSET=<seconds>,
  PRECISE=YES` and the master carries the query through to its
  `index.m3u8?from=<seconds>` variant. Without it a resuming player fetched
  segment 0 first (to lay out the timeline before honouring the seek), but the
  resume-first transcode produces segment 0 **last**, so playback blocked on a
  segment that would not exist for minutes — the observed "forever" before a
  long resume, and quality switches landing at 0:00. The segment list is
  unchanged (still the complete VOD list); a `from` outside `(0, duration)` adds
  no tag, and any `from`-specific playlist/master is served `no-store`. Clients
  pass `?from=` on the URL they hand the player, not only in the `POST`. See
  [api.md](api.md#compatible-video-renditions-hls).
- **HLS multivariant playlist for hls.js** (2026-08-27) — `hls_url` and every
  `hls_variants[].url` now point at `/media/hls/{id}/{height}/master.m3u8`, a
  one-entry master playlist carrying `#EXT-X-STREAM-INF` with `BANDWIDTH`,
  `CODECS` and `RESOLUTION`. Without it hls.js parsed the media playlist, saw
  the fragment count, and then never scheduled fragment 0: a codec-less media
  playlist gives it nothing to create the MSE `SourceBuffer` from. The `CODECS`
  string is parsed from the init segment the transcode writes (avcC → `avc1.…`,
  hvcC → `hvc1.…`, esds → `mp4a.40.2`), so it is truthful even for a copied
  source, and cached per rendition. The media playlist stays at `index.m3u8`
  (the master references it) for the native and byte-range paths; `AVPlayer`
  plays the master unchanged. See
  [api.md](api.md#compatible-video-renditions-hls).
- **Web: compatible renditions + quality picker** (2026-08-27) — the web client
  now uses the ladder it was built for. A codec gate
  (`frontend/src/player/codecGate.ts`) asks the browser what it can decode with
  `MediaSource.isTypeSupported` / `canPlayType` before a source is chosen, and
  runs the **same Auto rule as the Apple apps**: play the archive when this
  browser decodes it, otherwise the tallest rung at or below the screen's
  pixel height, skipping HEVC where there is no decoder for it. A *Quality*
  menu sits beside speed and subtitles — `Auto`, the source it stands for, and
  every offered rung with its own `state` — and the choice is kept per device
  in `localStorage`, never in `PATCH /me/prefs`. Safari plays the playlist
  natively; every other browser loads **hls.js in its own chunk**, imported
  only when a rendition is actually chosen, so the archive-direct path
  downloads nothing extra. `POST /videos/{id}/hls?height=&from=` starts the
  transcode at the resume position before the playlist is opened and re-aims
  it after a seek, and *Preparing a compatible version… 37 %* covers a
  rendition that has produced nothing yet. Switching quality keeps the clock;
  chapters, SponsorBlock, subtitles and heartbeats are untouched. See
  [design.md](design.md#player-resume-and-seen-state) and
  [api.md](api.md#compatible-video-renditions-hls).
- **Resume-first transcoding** (2026-08-27) — the compatible HLS rendition no
  longer makes a viewer resuming at 40:00 start at 0:00. The 4-second segment
  grid is fixed, so the playlist is generated by the server from the video's
  duration and is a **complete `EXT-X-PLAYLIST-TYPE:VOD` list from the very
  first request** — a player can seek anywhere in it before a frame has been
  encoded. `from=<seconds>` on `POST /videos/{id}/hls` (or on the playlist)
  points the encoder at the resume position: one run from there to the end,
  a second for the part before it, both cutting on the same forced-keyframe
  grid. A segment the encoder has not reached blocks until it lands
  (`MEDIA_SEGMENT_WAIT`) instead of 404ing, and a seek more than
  `MEDIA_SEEK_AHEAD_SEGMENTS` ahead re-aims the run. ffmpeg now reads the
  archive through a loopback HTTP source so `-ss` is a byte range rather than
  forty minutes of discarded decoding, with the TA token still never reaching
  a command line. `hls_variants[].hls_progress` reports how much of a rendition
  exists. See
  [api.md](api.md#compatible-video-renditions-hls).
- **Quality renditions** (2026-08-26) — the compatible HLS rendition is now a
  ladder: 2160, 1440, 1080, 720 and 480, capped at what the source holds, each
  its own cache entry derived only when a client asks for it. Above 1080p the
  codec is HEVC (`hevc_vaapi` / `libx265`, `hvc1`-tagged so AVFoundation takes
  it), because 4K H.264 is enormous and every Apple device that can show 4K
  decodes HEVC in hardware; 1080p and below stay H.264. The video detail
  carries `hls_variants` (height, url, state, codec, tallest first),
  `/media/hls/{id}/{height}/…` serves them, `POST /videos/{id}/hls?height=`
  prefetches one, and the old un-suffixed URLs still serve the 1080p entry for
  clients written before the ladder. See
  [api.md](api.md#compatible-video-renditions-hls) and
  [deploy.md](deploy.md#transcoding-cpu-and-the-media-cache).
- **Hardware transcoding** (2026-08-26) — the HLS rendition runs on an Intel
  iGPU through VAAPI when one is available: hardware decode, `scale_vaapi` and
  `h264_vaapi` with the frames never leaving the GPU, turning a 4K AV1 hour
  from tens of minutes of CPU into a few minutes. `MEDIA_HWACCEL` (`auto` by
  default) and `MEDIA_VAAPI_DEVICE` configure it, and it is never a hard
  dependency: no render node means the CPU path, and a source the hardware
  decoder refuses clears the partial rendition and re-runs in software rather
  than failing the request. See
  [deploy.md](deploy.md#hardware-acceleration-intel-vaapi).
- **Compatible video rendition** (2026-08-26) — `GET /media/hls/{id}/index.m3u8`
  serves the video transcoded to H.264/AAC as HLS with fMP4 segments, for the
  AV1 and VP9 in the archive that Apple hardware cannot decode. It is
  progressive: the transcode starts on the first request and the playlist is
  served as soon as the first segment exists, so playback starts in seconds
  rather than after the whole encode. A source that is already H.264 ≤1080p is
  copied, not re-encoded. Video detail carries `hls_url` and `hls_state`, and
  `POST /videos/{id}/hls` prefetches. `MEDIA_TRANSCODE_JOBS` caps concurrency.
  See [api.md](api.md#compatible-video-renditions-hls) and
  [deploy.md](deploy.md#transcoding-cpu-and-the-media-cache).
- **AAC audio variant** (2026-08-26) — `GET /media/audio/{id}.m4a` serves the
  same audio as AAC in MP4 beside the WebM/Opus one, so native Apple clients
  (which cannot decode Opus in WebM) have something to play for music
  playlists and as the fallback when a video codec is unplayable. It is a
  re-encode unless the source track is already AAC. Video detail carries it as
  `audio_aac_url`. See [api.md](api.md#derived-media).
- **Audio-only playback** (2026-08-26) — a derived-media cache remuxes the
  audio track on first request (~20–30× less data) and playlists can be marked
  audio-only for music. See [api.md](api.md#derived-media).
- **Pinned playlists** (2026-08-26) — pin a playlist to the sidebar from the
  playlist page or its card. Pins are Flimm's own per-user state, so they
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
  `GET /api/v1/config`. **Plan: [apple-apps.md](apple-apps.md)** — read it
  before starting; the codec question should be settled first. See
  [design.md](design.md#platforms) for the per-platform layout.

  *In progress:* the **iPhone, iPad and Apple TV apps** are built and live in `apple/`
  on the shared **FlimmKit** package (models, API client, OIDC + Keychain
  auth, playback context, progress heartbeat, WebVTT and chapter/SponsorBlock
  maths), with a `macos-26` CI job. It covers onboarding, the four sections,
  search, the feed and playlist editors, and a custom `AVPlayer` shell with
  resume, chapters, SponsorBlock, subtitles, Picture in Picture and
  audio-only playback. The **iPad layout** is done: a `NavigationSplitView`
  sidebar and adaptive grids over the very same screens, a player with the
  chapter list and up next beside the video, and the web client's keyboard
  shortcuts — one navigation model behind both shells, because iPad
  multitasking flips the size class mid-flow. The **tvOS app** is done too: a
  second target (`FlimmTV`) over the same package, a top tab bar with
  focus-driven grids, and `AVPlayerViewController` carrying Flimm's chapters
  as navigation markers and SponsorBlock as interstitials. Feeds are
  read-only there — editing stays on the phone, iPad and web. Sign-in is the
  **OIDC device authorization grant**, which the provider has to enable for
  the same client id (see [deploy.md](deploy.md#native-apps)); tvOS has no
  browser, so there is no fallback. Still to come: **TestFlight**.
  One backend gap turned up on the way — `/media/audio/{id}.webm` is Opus in
  WebM, which AVFoundation cannot decode — and is now closed: use
  `audio_aac_url` (`/media/audio/{id}.m4a`) on Apple platforms.

## Ideas

- **DeArrow** — crowd-sourced de-clickbaited titles and thumbnails, from the
  same project and the same hash-prefix API (`/api/branding/{prefix}`). The
  thumbnail half fits Flimm better than it fits a browser extension: DeArrow
  returns a *timestamp*, not an image, and Flimm holds the video file, so the
  server can cut that frame with ffmpeg through the derived-media cache instead
  of calling a third-party thumbnail service — no image fetch, works offline,
  and the same "the server decides so clients cannot drift" rule as the
  rendition ladder. Titles would be a preference in `PATCH /me/prefs` (original
  vs. improved), not a per-client toggle.
- **Scrub preview thumbnails** — a sprite sheet and a WebVTT track derived once
  per video into the media cache. Web and the phone/iPad draw it above the
  scrubber; tvOS gets it for free, because `AVPlayerViewController` renders
  trick-play images natively.
- **Loudness normalisation** — one EBU R128 (`loudnorm`) analysis pass per
  video, the gain stored and applied by the player, so channels stop being at
  wildly different volumes. Matters most for the audio-only music path.
- **Silence and black-frame detection** — an intro/outro heuristic for the long
  tail of videos SponsorBlock has never seen, derived locally.
- **Transcripts (Whisper)** — generate subtitles for videos the archive has
  none for, and index them for search *inside* a video. The heaviest item here
  by a wide margin, and the only one that unlocks a new kind of search.
- **Return YouTube Dislike** — dislike counts, which TA does not index (it
  carries `view_count` and `like_count` only). Ranked last deliberately: its
  API takes a bare video id with no hash prefix, so every video detail view
  would leak watch history to a third party. It would have to be off by
  default and called out in [deploy.md](deploy.md).
- **"Most replayed" heatmap** — yt-dlp exposes YouTube's heatmap, which would
  tint the scrubber beside the SponsorBlock segments. It needs a live YouTube
  request per video, though, so the honest place to capture it is at download
  time on the TubeArchivist side, not a per-view fetch from Flimm.
- **Comments** — render TubeArchivist's archived comments under the video.
- **Download queue management** — view and manage TubeArchivist's download
  queue and subscriptions from Flimm (add URLs, retry, ignore).
- **Multi-instance** — one Flimm backend fronting several TubeArchivist
  instances, with feeds spanning them.
