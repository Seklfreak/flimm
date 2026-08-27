# Roadmap

## Done

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

- **SponsorBlock UI** — per-category skip settings and a manual skip button.
  Segments already render on the timeline, and the highlight is already a jump.
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
