# Roadmap

## Done

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
  See [api.md](api.md#compatible-video-rendition-hls) and
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
  Segments already render on the timeline.
- **Comments** — render TubeArchivist's archived comments under the video.
- **Download queue management** — view and manage TubeArchivist's download
  queue and subscriptions from Flimm (add URLs, retry, ignore).
- **Multi-instance** — one Flimm backend fronting several TubeArchivist
  instances, with feeds spanning them.
