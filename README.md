# Flimm

A self-hosted client for a single [TubeArchivist](https://github.com/tubearchivist/tubearchivist)
instance. TubeArchivist downloads and indexes your YouTube channels; Flimm is
the place you *watch* them: feeds built from named sets of channels, automatic
resume, per-user history, subtitle search, and a clean player — with every
piece of state that TubeArchivist can hold (watched flag, resume position,
custom playlists) written back to it, so the stock TA UI stays consistent.

One container image: a Go backend with the React frontend embedded.

<!-- screenshots: docs/screenshots/{feed,player,search}.png -->
> Screenshots coming soon.

## Features

- **Feeds** — named sets of channels (*Home*, *DevOps*, *Making*…) with unseen
  counts, per-feed sort / hide-seen / Shorts / subtitles-only options, one
  pinned feed the app opens on, and a built-in **Everything** feed. A feed can
  also hold a single **series** — one of a channel's playlists — so one Let's
  Play follows you around without the channel's other thousands of videos
  (TubeArchivist must have the playlist indexed; an admin can trigger that
  from the channel page).
- **Channels** directory — search, sort, see which feeds each channel is in,
  find channels in no feed; add/remove from feeds right on the channel page.
- **Resume & seen state per user** — heartbeat progress, automatic resume
  with *Start over*, auto-seen at ~90 %, *Mark seen* / *Mark unseen*, mark a
  whole feed or channel seen.
- **History** — grouped by day, in-progress rows resume in place, entries can
  be hidden.
- **Not interested** — take a video out of every feed without watching it. It
  is not "mark seen": watch state is untouched and nothing is written back to
  TubeArchivist. Undo is one tap, and a dismissed video stays on its channel
  page so it can be put back later.
- **Playlists** — your custom playlists and channel playlists with watched
  progress and a resume target; create, reorder, delete through TubeArchivist.
- **Search** across titles, channels, playlists **and subtitle text**; a
  subtitle hit jumps straight to that timestamp.
- **Player** — archived and auto-generated subtitle tracks, SponsorBlock
  segment skipping, scrub previews (drag the scrubber and see the frame you
  are dragging to), loudness normalisation, playback speed, autoplay with
  context-aware *Up next*, an end card when a video finishes without autoplay
  taking over (what is next, and a replay), and the archived comments (folded
  away until you ask, and no avatars fetched from Google).
- **Playback stats** (web) — a panel under the video saying what the player is
  actually doing: whether the archived file is playing directly or a rendition
  is, *why* the gate chose that, how far the transcode has got, whether the
  scrub-preview sheet and the loudness pass are ready or still being derived,
  and what the video element itself reports (buffer ahead, dropped frames,
  picture size). Everything the server derives is invisible by design, and
  each of those jobs fails in a way that looks exactly like nothing happening.
- **An even volume across channels** — each video is measured once (EBU R128)
  and the loud ones are turned down to a common target, so you stop reaching
  for the volume between one channel and the next. The measurement is made
  from your own copy of the file and nothing is re-encoded; the gain is
  decided on the server, so every client applies the same one.
- **Settings** — autoplay, speed, sponsor skipping, subtitle language and size,
  the Everything-feed options and the theme, all stored per user and shared
  with the native apps.
- **SponsorBlock, fetched live** — the backend asks the SponsorBlock service
  itself rather than reading TubeArchivist's download-time snapshot, so
  segments stay current, `mute` segments mute instead of skipping, the
  crowd-sourced *highlight* is one click away, and chapter names fill in for
  videos whose file carries none. The
  lookup sends a hash prefix of the video id, never the id, and
  `SPONSORBLOCK_URL=` turns it off. See
  [docs/api.md](docs/api.md#sponsorblock).
- **Plays what your devices can't** — the archive is full of AV1 and VP9 that
  Apple hardware cannot decode and that not every browser does either, so Flimm
  derives a compatible **HLS** rendition on demand and streams it segment by
  segment: playback starts within seconds instead of after the whole transcode.
  Audio-only renditions come the same way. See
  [docs/api.md](docs/api.md#derived-media).
- **Pick a quality** — up to five renditions per video (2160, 1440, 1080, 720,
  480, capped at what the source holds): H.264 up to 1080p, HEVC above it, so
  4K plays on anything from an iPhone 7 or an Apple TV 4K on. Each height is
  derived only when a client asks for it. Every client — the web player and the
  Apple apps — offers the same *Quality* menu and the same *Auto* rule: the
  archived file when the device decodes it, and otherwise the tallest rendition
  the screen can show. The choice is remembered per device. See
  [docs/api.md](docs/api.md#compatible-video-renditions-hls).
- **Preferences** per user (autoplay, speed, subtitles, theme…).
- **OIDC login** with any provider; media streams through the backend so
  TubeArchivist itself never has to be exposed.

See [docs/design.md](docs/design.md) for the product model and
[docs/apple-apps.md](docs/apple-apps.md) for the native clients.

## Architecture

```mermaid
flowchart LR
  subgraph clients [Clients]
    Web["Web app<br/>(React, embedded)"]
    Native["iPhone · iPad · Apple TV<br/>(SwiftUI, one shared package)"]
  end
  subgraph flimm [Flimm container]
    API["Go backend<br/>/api/v1 · /media"]
  end
  PG[(Postgres<br/>users · feeds · watch events · history · prefs)]
  OIDC[["OIDC provider<br/>Authentik / Keycloak / Auth0 …"]]
  TA["TubeArchivist<br/>API · media · thumbnails · subtitle index"]

  Web -- "Bearer JWT / media cookie" --> API
  Native -- "Bearer JWT" --> API
  Web -. "Auth Code + PKCE" .-> OIDC
  API -. "validate JWT" .-> OIDC
  API --> PG
  API -- "Authorization: Token" --> TA
```

Clients talk **only** to the Flimm backend. The backend validates OIDC
tokens, keeps the state TubeArchivist lacks in Postgres, reads everything else
live from TubeArchivist with a server-side API token, and reverse-proxies video
(with range requests), subtitles and thumbnails.

### Repository layout

| Path | What |
|---|---|
| `cmd/server`, `internal/` | the Go backend — `/api/v1`, the `/media` proxy, the TubeArchivist client, migrations |
| `frontend/` | the React web app, embedded into the binary |
| `apple/` | the native iPhone/iPad and Apple TV apps and `FlimmKit`, the Swift package they share — see [docs/apple-apps.md](docs/apple-apps.md) and [apple/README.md](apple/README.md) |
| `docs/` | [api.md](docs/api.md) (the normative contract), design, deploy, apple-apps |

The API contract and every client live in one repository on purpose: they
change together.

## How it maps onto TubeArchivist

| Flimm | TubeArchivist |
|---|---|
| Video lists, channels, playlists, search, similar, comments | read live from the TA API (short per-user caches) |
| Watched flag | `POST /api/watched/` — written on every change |
| Resume position | `POST/DELETE /api/video/{id}/progress/` — written on every heartbeat |
| Custom playlists | created / reordered / deleted via `/api/playlist/custom/` |
| Feeds, history, prefs, per-user watch events | **Flimm's Postgres only** — TA has no per-user model |
| Video, subtitles, thumbnails | proxied from TA's `/media` and `/cache` with the API token |

The full contract — every endpoint, object and the exact TA calls behind them —
is in [docs/api.md](docs/api.md).

## Requirements

- A **TubeArchivist** instance (v0.4+) and an **API token** for it
  (Settings → User). Ideally reachable over the network from Flimm without an
  auth proxy in between.
- **Postgres** 15+.
- An **OIDC provider** — any: Authentik, Keycloak, Auth0, Zitadel, Dex,
  Google… Flimm needs a public (PKCE) client with redirect URI
  `https://<host>/auth/callback`.
- HTTPS in front of Flimm (the media cookie is `Secure`).
- **ffmpeg** for derived media. It ships in the container image; a local build
  needs it on `PATH` (or set `FFMPEG_PATH`). Without it everything else works
  and only `/media/audio/*` and `/media/hls/*` fail. The WebM rendition
  browsers use is a stream copy and nearly free; the `.m4a` (AAC) one native
  Apple clients need is a real re-encode; the **HLS video renditions are a real
  transcode** (x264, or x265 above 1080p), so give the container several cores
  if clients use them. An Intel iGPU on the host takes that transcode off the
  CPU — optional, off unless the
  render node is there, see
  [docs/deploy.md](docs/deploy.md#hardware-acceleration-intel-vaapi). See also
  [docs/api.md](docs/api.md#derived-media) and
  [docs/deploy.md](docs/deploy.md).

## Configuration

All configuration is via environment variables.

| Var | Required | Notes |
|---|---|---|
| `TA_URL` | yes | e.g. `http://tubearchivist:8000` (in-cluster) or the public URL |
| `TA_TOKEN` | yes | TubeArchivist API token (Settings → User) |
| `DATABASE_URL` | yes | Postgres DSN |
| `MEDIA_TOKEN_SECRET` | yes | HMAC secret for the media cookie (`openssl rand -hex 32`) |
| `PUBLIC_URL` | yes | the `https://` origin users reach Flimm on; used for cookies/CORS |
| `OIDC_ISSUER` | unless `AUTH_DISABLED=true` | issuer URL (discovery at `<issuer>/.well-known/openid-configuration`) |
| `OIDC_CLIENT_ID` | unless `AUTH_DISABLED=true` | public client id |
| `AUTH_DISABLED` | no | `true` skips auth and uses a fixed dev user — **dev only** |
| `ADMIN_EMAILS` | no | comma-separated; admins see `/healthz` details and get the archive-side controls (index a channel's series, subscribe/unsubscribe) |
| `APP_NAME` | no | default `Flimm` |
| `PORT` | no | default `8080` |
| `MIN_PLAY_SECONDS` | no | how long a video must play before it enters history and gets a resume position (default 15) |
| `MEDIA_TOKEN_SECONDS` | no | how long a signed media token stays valid; default 2592000 (30 days) |
| `MEDIA_CACHE_DIR` | no | where derived renditions are cached; default a temp dir. Must be writable — an HLS rendition of a 1080p hour is ~2–3 GB, a 2160p HEVC one ~6–8 GB |
| `MEDIA_CACHE_MAX_BYTES` | no | cache size cap before least-recently-used eviction (default 5 GiB) |
| `MEDIA_TRANSCODE_JOBS` | no | concurrent HLS transcodes (default 1), counted across every video and height; extra requests queue. Scrub-preview and loudness passes run in a lane of their own and are not counted against it |
| `MEDIA_SEGMENT_WAIT` | no | seconds a request for an HLS segment the transcode has not produced yet waits before the client is told to come back (default 60) |
| `MEDIA_SEEK_AHEAD_SEGMENTS` | no | how far ahead of the encoder (in 4-second segments) a segment request has to be before the running transcode is re-aimed at it (default 30, about two minutes) |
| `FFMPEG_PATH` | no | ffmpeg binary; default `ffmpeg` on `PATH` |
| `MEDIA_HWACCEL` | no | hardware transcoding: `auto` (default — Intel VAAPI when a render node is there and openable, CPU otherwise), `vaapi`, or `off`. A failed hardware attempt falls back to the CPU per video. See [docs/deploy.md](docs/deploy.md#hardware-acceleration-intel-vaapi) |
| `MEDIA_VAAPI_DEVICE` | no | DRM render node for VAAPI; default `/dev/dri/renderD128` |
| `SPONSORBLOCK_URL` | no | SponsorBlock server segments are fetched from; default `https://sponsor.ajay.app`. Set it **empty** to disable the lookup and use TubeArchivist's download-time snapshot instead (an offline deploy) |
| `SPONSORBLOCK_CATEGORIES` | no | comma-separated list restricting what is asked for; by default everything the service offers is fetched and each client decides what to do with it |
| `DEARROW_URL` | no | DeArrow server for crowd-sourced titles and thumbnails; default `https://sponsor.ajay.app`. Set it **empty** to disable the lookup for the whole deployment. Nothing is asked for until a viewer turns titles or thumbnails on in Settings — both are off by default — and the lookup sends a hash prefix, never a video id |
| `RYD_URL` | no | [Return YouTube Dislike](https://returnyoutubedislike.com) API for dislike counts — **empty (off) by default**. Set it to `https://returnyoutubedislikeapi.com` to switch it on. Unlike SponsorBlock and DeArrow, this service is asked about a video **by id**, so it learns what this deployment is watching; that is why it is opt-in. See [deploy.md](docs/deploy.md#outbound-network-sponsorblock-and-the-one-that-is-off) |
| `SENTRY_DSN` | no | backend error reporting **and tracing**. Every `/api/v1` request is traced, named after its route (`GET /api/v1/feeds/{id}/videos`), with a span per database query and per outgoing call to TubeArchivist, SponsorBlock or DeArrow — which is how a slow request is read as "waiting on that" rather than as unexplained time. Media streaming, health checks and static files are not traced |
| `VITE_SENTRY_DSN` | no | frontend error reporting; a **build arg**, baked into the bundle at image build time (not a runtime env var) |
| `VITE_UMAMI_URL`, `VITE_UMAMI_WEBSITE_ID` | no | self-hosted [Umami](https://umami.is) analytics for the web app; **build args**, baked in at image build time. See [Analytics](#analytics) |
| `ANALYTICS_DISABLED` | no | `true` turns analytics off for every client of this deployment at runtime, whatever they were built with. See [Analytics](#analytics) |
| `LOG_LEVEL` | no | `debug`, `info` (default), `warn`, `error` |

## Running locally

You need Go 1.26+, Node 24 and Docker.

```sh
# 1. Postgres
docker compose up -d postgres        # or any local Postgres

# 2. Backend (auth off, dev user)
export TA_URL=https://tubearchivist.example.com
export TA_TOKEN=…
export DATABASE_URL=postgres://archive:archive@localhost:5432/archive?sslmode=disable
export MEDIA_TOKEN_SECRET=dev-only-not-secret
export PUBLIC_URL=http://localhost:5173
export AUTH_DISABLED=true
go run ./cmd/server                  # http://localhost:8080

# 3. Frontend with hot reload (proxies /api and /media to :8080)
cd frontend && npm ci && npm run dev  # http://localhost:5173
```

### Without a TubeArchivist: the fake

`cmd/fake-ta` is a stand-in TubeArchivist: the subset of TA's API Flimm calls,
over a small fixed catalogue, with the media files generated by ffmpeg on first
run so videos really play, seek and resume. Nothing it holds is written to a
real archive, which is what makes it safe to click around in.

```sh
go run ./cmd/fake-ta                 # :8001, generates media on first run
export TA_URL=http://localhost:8001
export TA_TOKEN=dev                  # any value; the fake accepts them all
```

It serves 13 videos over 4 channels: chapters embedded in the files, SponsorBlock
segments, WebVTT subtitles whose lines name their own timestamp, a custom
playlist, and one VP9 video that Apple hardware cannot decode — so the codec
gate and the compatible HLS rendition get exercised too. The picture is a
running timer, so a seek, a resume or a chapter jump can be checked by eye.

Set `SPONSORBLOCK_URL=` (empty) alongside it: the real SponsorBlock service has
never heard of these video ids, and its answer of "no segments" is
authoritative, so the fake's own segments only show with the lookup turned off.

The **native apps work against this too**: with `AUTH_DISABLED=true` the server
publishes `auth_disabled` on `/api/v1/config`, and the iPhone, iPad and Apple TV
apps connect with no sign-in step (`http://localhost:8080` in a simulator).

`make build` produces the single binary with the frontend embedded;
`make sqlc` regenerates the query code after editing `internal/db/queries`.
With `AUTH_DISABLED=true` the media cookie is still issued but not marked
`Secure`, so plain-HTTP localhost works.

Tests: `go test ./...` and `cd frontend && npm run lint && npm run build`.

## Container image

```
ghcr.io/seklfreak/flimm:<version>   # semver, e.g. 1.2.3
ghcr.io/seklfreak/flimm:latest      # newest release
```

`linux/amd64`. Listens on `PORT` (8080), serves the API, media proxy and web
app from one port, runs migrations on start.

## Analytics

Flimm can report anonymous usage to a self-hosted
[Umami](https://umami.is) instance: a pageview per screen and four events
(`play`, `search`, `feed-created`, `sign-in`). It is **deliberately
incurious** — screens are reported as route patterns (`/watch/:id`, never a
video id), and no event carries a video, channel, playlist, search term or
account. The native apps identify a visitor by a random UUID minted per
install; nothing touches the IDFA.

Where the endpoint comes from:

- **Web** — `VITE_UMAMI_URL` and `VITE_UMAMI_WEBSITE_ID`, baked into the bundle
  at image build time, like the Sentry DSN. Build your own image without them
  and the tracker is never loaded.
- **iPhone / iPad / Apple TV** — `UMAMI_URL` and `UMAMI_WEBSITE_ID` in
  `apple/Config/Secrets.xcconfig` (CI passes them from repo variables). Debug
  builds never report.

**The prebuilt `ghcr.io/seklfreak/flimm` image and the TestFlight apps are
built with the maintainer's endpoint**, so a deployment running them reports
to it unless you say otherwise. Two ways to say otherwise: set
`ANALYTICS_DISABLED=true` on the server — every client asks
`GET /api/v1/config` and honours it, including the App Store apps — or build
the image yourself and leave the build args unset.

## Deploying

Flimm is a stateless single container next to Postgres and TubeArchivist.
[docs/deploy.md](docs/deploy.md) has a generic Kubernetes example (Deployment,
Service, Ingress, Secret, ConfigMap) plus notes on placing it in the same
cluster as TubeArchivist so streaming doesn't cross an auth proxy, OIDC
redirect URIs, and the HTTPS requirement. Docker Compose works the same way:
one service, the env vars above, HTTPS from your reverse proxy.

## Roadmap

The **native Apple apps** are built and live in `apple/` on the shared
`FlimmKit` package: an iPhone/iPad app (tab bar and split view over one
navigation model, a custom `AVPlayer` shell with resume, chapters,
SponsorBlock, subtitles, Picture in Picture and audio-only playback) and an
**Apple TV** app (focus-driven grids and `AVPlayerViewController`, signing in
with the OIDC device authorization grant since tvOS has no browser, and the
pinned feed on the Home screen's top shelf). Both ship to TestFlight on every
version tag — see [docs/apple-apps.md](docs/apple-apps.md).

## Notice

Not affiliated with TubeArchivist.
