# Flimm API contract (v1)

Flimm is a client for a single [TubeArchivist](https://github.com/tubearchivist/tubearchivist)
instance. All clients (web, iOS, iPadOS, tvOS) talk **only** to the Flimm backend;
the backend talks to TubeArchivist (TA) with a server-side API token. The backend
adds the state TA lacks (feeds, history, preferences) and writes everything TA
*can* hold (watched flag, resume position, custom playlists) back to TA so the
stock TA UI stays consistent.

Base path: `/api/v1`. JSON, snake_case. Times are RFC 3339 UTC. IDs are strings.

## Authentication

- Clients obtain an OIDC access token (Authorization Code + PKCE) from the
  configured issuer and send `Authorization: Bearer <jwt>` on every `/api/v1/*`
  request. The backend validates the JWT against `OIDC_ISSUER` / `OIDC_CLIENT_ID`
  and upserts a `users` row keyed by the token `sub`.
- `AUTH_DISABLED=true` (dev only) skips validation and uses a fixed dev user.
- Media (`/media/*`) is fetched by `<video>` / `AVPlayer`, which cannot always
  set headers, so media accepts **either** a Bearer header **or** an `flimm_media`
  cookie. `POST /api/v1/session/media` (authenticated) sets that HttpOnly,
  SameSite=Lax, Secure cookie containing a signed, short-lived (12h) media token;
  the web app calls it after login and again when a media request returns 401.
  Native clients pass the Bearer header via `AVURLAssetHTTPHeaderFieldsKey`.
- OIDC discovery for clients: `GET /api/v1/config` (unauthenticated) returns
  `{ "app_name", "oidc_issuer", "oidc_client_id", "version" }` so native apps
  need only the server URL.

## Errors

`{ "error": "message" }` with a meaningful status. Unknown/unauthorized resources
return 404 so existence isn't leaked. 502 when TA is unreachable, with
`{ "error": "tubearchivist unavailable" }`.

## Common objects

### VideoSummary
```json
{
  "id": "yt-id",
  "title": "…",
  "channel": { "id": "UC…", "name": "…", "thumb_url": "/media/thumb/channel/UC…" },
  "thumb_url": "/media/thumb/video/yt-id",
  "duration": 1476,
  "published": "2026-08-23T00:00:00Z",
  "downloaded": "2026-08-23T04:12:00Z",
  "type": "video|short|stream",
  "subtitle_langs": ["en"],            // archived tracks; [] if none
  "has_auto_subtitles": true,
  "watched": false,                    // TA watched flag
  "position": 561,                     // resume position in seconds, 0 if none
  "progress": 0.38,                    // position/duration, 0 or 1 when watched
  "last_played_at": "2026-08-26T15:42:00Z"  // null if never played here
}
```
`watched`, `position`, `progress`, `last_played_at` are per-user and come from
Flimm's `watch_events` table, falling back to TA's watched flag when Flimm
has no event.

Any `position > 0` on an unwatched video means "in progress": the card shows a
`Resume · m:ss` pill and **every** link to the player (thumbnail, title, Resume
button) resumes from it. Clients must not require a `t=` parameter to resume —
resume is the default action, and `t=` only exists to jump to a subtitle hit.

### Video (detail) — VideoSummary plus
```json
{
  "description": "…",
  "height": 1080,
  "media_url": "/media/video/yt-id.mp4",
  "audio_url": "/media/audio/yt-id.webm",   // derived on first request; see Derived media
  "youtube_url": "https://www.youtube.com/watch?v=yt-id",
  "subtitles": [ { "lang": "en", "source": "user|auto", "url": "/media/subtitles/yt-id/en.vtt" } ],
  "sponsorblock": [ { "category": "sponsor", "start": 12.3, "end": 45.6 } ],
  "stats": { "views": 0, "likes": 0 },
  "tags": [],
  "playlists": [ { "id": "PL…|custom-id", "name": "…", "position": 9, "count": 14 } ],
  "channel": { …ChannelSummary… }
}
```

### ChannelSummary
```json
{
  "id": "UC…", "name": "…", "thumb_url": "…", "banner_url": "…",
  "video_count": 212, "unseen_count": 3,
  "last_upload": "2026-08-25T00:00:00Z",
  "subscribed": true,
  "feeds": [ { "id": "…", "name": "Home" } ]
}
```

### Feed
```json
{
  "id": "uuid" | "everything",
  "name": "Home",
  "channel_ids": ["UC…"],              // empty + id "everything" = all channels
  "channel_count": 6,
  "unseen_count": 7,
  "sort": "newest|oldest|shortest|longest",
  "hide_seen": true,
  "include_shorts": false,
  "subtitles_only": false,
  "pinned": true,                      // at most one; the feed the app opens on
  "position": 0,                       // sidebar order
  "created_at": "…", "updated_at": "…"
}
```
`everything` is built-in: read-only except `sort`/`hide_seen`/`include_shorts`
(stored in prefs), always last.

### PlaylistSummary
```json
{
  "id": "…", "name": "…", "kind": "custom|channel",
  "channel": { "id": "…", "name": "…" } | null,
  "thumb_url": "…",
  "video_count": 14, "total_duration": 15120,
  "seen_count": 11, "in_progress_count": 1,
  "progress": 0.78,
  "resume_video_id": "yt-id" | null,   // first in-progress, else first unseen
  "pinned": false,                     // shown in the client's sidebar
  "audio_only": false                  // play this playlist as audio (music)
}
```

#### Pinned playlists

Pins are Flimm's own state (TubeArchivist has no concept of them) and are
per user, so they follow the account across web and native clients. A pin
survives the playlist being renamed or reordered, and pinning a playlist that
is later deleted in TubeArchivist simply drops out of
`GET /playlists/pinned` — the endpoint only returns playlists that still
resolve, so a stale pin can never wedge the sidebar.

### HistoryEntry
```json
{
  "id": "uuid",
  "video": VideoSummary,
  "played_at": "…",                    // last_played_at
  "state": "in_progress|seen"
}
```

### Page
List endpoints take `page` (0-based) and `page_size` (default 30, max 100) and
return `{ "items": [...], "page": 0, "page_size": 30, "total": 123 }`.

## Endpoints

### Meta / session
| Method | Path | Notes |
|---|---|---|
| GET | `/config` | unauthenticated; app name, OIDC issuer/client id, version |
| GET | `/healthz` | unauthenticated; 200 when DB ok; `ta` field reports TA reachability |
| GET | `/me` | `{ "id", "name", "email", "is_admin", "prefs": Prefs }` |
| PATCH | `/me/prefs` | partial update of Prefs, returns Prefs |
| POST | `/session/media` | sets `flimm_media` cookie, 204 |

Prefs:
```json
{
  "autoplay": true,
  "playback_speed": 1.0,
  "subtitle_lang": "en",               // language code, or "off"; defaults to "en"
  "subtitle_size": "small|medium|large",
  "skip_sponsors": true,
  "everything_sort": "newest", "everything_hide_seen": true, "everything_include_shorts": false,
  "theme": "system|light|dark"
}
```

### Feeds
| Method | Path | Notes |
|---|---|---|
| GET | `/feeds` | all feeds incl. `everything`, ordered by `position`, with unseen counts |
| POST | `/feeds` | body: name, channel_ids, options → Feed (201) |
| GET | `/feeds/{id}` | |
| PUT | `/feeds/{id}` | full update; `pinned:true` unpins the others |
| DELETE | `/feeds/{id}` | 204; never touches channels/videos |
| POST | `/feeds/reorder` | `{ "ids": [...] }` |
| GET | `/feeds/{id}/videos` | query `view=unseen\|continue\|all` (default: feed's `hide_seen` → unseen else all), paged |
| POST | `/feeds/{id}/mark-seen` | marks every currently unseen video in the feed watched; 204 |

### Channels
| Method | Path | Notes |
|---|---|---|
| GET | `/channels` | query `q`, `sort=name\|videos\|unseen\|last_upload`, `unfeeded=true`; paged ChannelSummary |
| GET | `/channels/{id}` | ChannelSummary + `description` |
| GET | `/channels/{id}/videos` | `view=all\|unseen`, `sort`, paged |
| GET | `/channels/{id}/playlists` | PlaylistSummary[] |
| PUT | `/channels/{id}/feeds` | `{ "feed_ids": [...] }` — the "In feeds:" control |
| POST | `/channels/{id}/mark-seen` | 204 |

### Videos
| Method | Path | Notes |
|---|---|---|
| GET | `/videos/{id}` | Video detail |
| GET | `/videos/{id}/up-next` | query `feed=<id>` or `playlist=<id>` or `channel=<id>`; **paged** VideoSummary of everything following the video in that context, falling back to `similar` when nothing follows. Paged so a long playlist scrolls rather than being truncated |
| GET | `/videos/{id}/nav` | same context query; `{ "index", "total", "previous", "next" }` for stepping through the list in both directions |
| GET | `/videos/{id}/similar` | VideoSummary[] (TA similar) |
| GET | `/videos/{id}/comments` | TA comments passthrough |
| GET | `/videos/{id}/chapters` | chapter markers for the scrubber (see below); cached per video |
| POST | `/videos/{id}/progress` | `{ "position": 561 }` — heartbeat. Upserts watch_event; writes TA `/video/{id}/progress/`; at ≥90% (or ≤30 s remaining) marks watched. Returns `{ "position", "watched" }`. **Nothing is recorded below `MIN_PLAY_SECONDS`** unless the video completes or an event already exists — see below |
| POST | `/videos/{id}/watched` | `{ "watched": true\|false }` — writes TA `/watched/`; true completes the watch_event, false clears position and TA progress |
| DELETE | `/videos/{id}/progress` | "Start over": position → 0, TA progress deleted, 204 |

#### Nav

```json
{ "index": 8, "total": 14, "previous": VideoSummary|null, "next": VideoSummary|null }
```

`first` is the head of the list — the entry point for a shuffled run, so a
client never has to derive the shuffled order itself.

The context is the same `feed` / `playlist` / `channel` query `up-next` takes,
and the ordering is identical — `nav` is the same list, addressed by position
rather than sliced. `previous` and `next` are `null` at the ends of the list
(no wrap-around) and `index` is `-1` when the video isn't in the list at all,
either because it was opened without a context or because it has dropped out
of a "hide seen" feed since. Clients hide the step controls when there is no
context, and disable a single button at the ends.

#### Shuffle

Both `up-next` and `nav` accept `shuffle=<seed>`, an opaque string. The server
orders the context list by `hash(seed, video id)` rather than permuting
positions, which buys two properties the client depends on:

- **Deterministic** — the same seed always yields the same order, so
  previous/next, autoplay and the up-next panel agree with each other and
  survive a reload or a shared link.
- **Stable under change** — an item appearing or disappearing (a playlist
  edit, a video newly marked seen in a hide-seen feed) leaves the order of
  everything else untouched, so the running order doesn't scramble mid-play.

There is no server-side shuffle state: the seed in the URL *is* the shuffle.
Reshuffling means picking a new seed. A client starts a run by requesting
`nav` with the seed and jumping to `first`.

#### Chapters

```json
{
  "source": "embedded|description|none",
  "chapters": [ { "start": 0, "end": 132.5, "title": "Intro" } ]
}
```

TubeArchivist stores no chapters, so Flimm derives them, preferring the
authoritative source:

1. **`embedded`** — yt-dlp embeds YouTube's chapters into the container at
   download time. The backend range-fetches the `moov` box (files are
   faststart, so it sits at the front) and reads the Nero `chpl` box, falling
   back to the QuickTime chapter text track referenced by `tref`/`chap`.
2. **`description`** — timestamp lines in the description (`0:00 Intro`,
   `1:02:03 - Something`). Used only when nothing is embedded, and only when at
   least two timestamps parse and they increase monotonically.
3. **`none`** — `chapters` is `[]`.

`end` is the next chapter's `start`, and the last chapter ends at the video
duration. Times are seconds (float). Titles are trimmed and never empty.
Clients treat an empty list as "no chapter UI", never as an error.

### Playlists
| Method | Path | Notes |
|---|---|---|
| GET | `/playlists/pinned` | PlaylistSummary[] the user pinned to the sidebar, in `position` order; unpaged |
| PUT | `/playlists/{id}/pinned` | `{ "pinned": true\|false }` → 204. Pinning appends to the end; unpinning closes the gap |
| PUT | `/playlists/{id}/audio-only` | `{ "audio_only": true\|false }` → 204. Clients open this playlist's videos in audio mode |
| GET | `/playlists` | query `kind=custom\|channel`, paged PlaylistSummary; custom first |
| POST | `/playlists` | `{ "name" }` → TA `/playlist/custom/` (201) |
| GET | `/playlists/{id}` | PlaylistSummary + `items: [{ "position", "video": VideoSummary }]` |
| PATCH | `/playlists/{id}` | `{ "name" }` custom only (rename is client-side + TA re-create if TA has no rename; document limitation) |
| DELETE | `/playlists/{id}` | custom only; TA delete without videos; 204 |
| POST | `/playlists/{id}/videos` | `{ "video_id", "action": "add\|remove\|up\|down\|top\|bottom" }` → TA custom playlist actions |

### History
| Method | Path | Notes |
|---|---|---|
| GET | `/history` | query `filter=all\|in_progress\|seen`, `q` (title/channel substring), paged HistoryEntry, newest first. Entries below `MIN_PLAY_SECONDS` that never completed are excluded |
| DELETE | `/history/{entry_id}` | hides the entry (soft delete), 204; does not change watched state |

#### Minimum play time

A video opened by accident should leave no trace, so a watch event is only
created once `MIN_PLAY_SECONDS` (default 15) of playback is reported. Below
that the heartbeat is accepted and echoed but nothing is written — no history
entry, no resume position, and no progress pushed to TubeArchivist.

Two exceptions keep the rule from losing real views:

- **Completion always records**, so a video shorter than the threshold can
  still reach history.
- **An existing event keeps updating**, so scrubbing back to the start of a
  video you have already watched moves the position instead of orphaning it.

`GET /history` additionally filters out never-completed entries below the
threshold, so entries written before the threshold existed disappear from the
list without being deleted.

### Search
`GET /search?q=…&scope=all|titles|subtitles|channels|playlists&unseen=true&feed=<id>`
```json
{
  "took_ms": 80,
  "videos":   { "total": 11, "items": [ { "video": VideoSummary, "subtitle_hits": [ { "lang": "en", "start": 400.2, "end": 404.9, "text": "…enable input shaping…" } ] } ] },
  "channels": { "total": 1, "items": [ ChannelSummary + "match_count" ] },
  "playlists":{ "total": 2, "items": [ PlaylistSummary + "match_count" ] }
}
```
Implementation: TA `/search/?query=` for titles/channels/playlists and
`full:<q>` for the `ta_subtitle` index; group subtitle hits by video; `unseen`
and `feed` filter the video results in the backend.

### Media (no `/api/v1` prefix)
| Path | Notes |
|---|---|
| `GET /media/video/{id}.mp4` | reverse-proxies TA `/media/<media_url>` with `Range`, `If-Range`, `Accept-Ranges`, `Content-Length`, `Content-Type` passthrough |
| `GET /media/audio/{id}.webm` | audio-only stream, derived and cached on first request (see below); supports `Range` |
| `GET /media/subtitles/{id}/{lang}.vtt` | TA subtitle track |
| `GET /media/thumb/video/{id}` | TA `/cache/videos/…` |
| `GET /media/thumb/channel/{id}` and `/media/thumb/channel/{id}/banner` | TA `/cache/channels/…` |
| `GET /media/thumb/playlist/{id}` | TA `/cache/playlists/…` |

Thumbnails are cacheable (`Cache-Control: private, max-age=86400`).

The proxy rewrites `Content-Type` from the file extension when TA returns
`application/octet-stream`, and sets `Accept-Ranges: bytes` on 200/206. TA's
nginx declares a `types { text/vtt vtt; }` block on `/media/`, which replaces
the default MIME map for that location, so `.mp4` would otherwise arrive as
`application/octet-stream` and `<video>` refuses to decode it.

### Derived media

TubeArchivist stores one file per video, muxed. Anything else a client needs —
audio only today, an Apple-compatible rendition later — is *derived* from that
file and cached on disk, keyed by `(video id, variant)`.

- `GET /media/audio/{id}.webm` is the `audio` variant. The archived audio is
  already Opus, so it is **remuxed, not re-encoded** (`-vn -c:a copy`): no
  quality loss, negligible CPU, and roughly 20–30× less data than the source
  (a 40-minute 1080p video is ~1.2 GB muxed and ~37 MB as audio).
- The first request for a variant produces it; concurrent requests for the same
  variant wait on that one job rather than each starting their own.
- Once produced, the file is served from disk with full `Range` support, so
  seeking and resume behave exactly like the video stream.
- The cache is bounded by `MEDIA_CACHE_MAX_BYTES` and evicted least-recently-
  used. It is a cache in the strict sense: deleting it costs only the CPU to
  re-derive, so it can live on ephemeral storage.
- Derivation reads the source from TubeArchivist over HTTP with the API token;
  nothing is written back to TA.

Clients choose the stream. `audio_only` on a playlist is the persisted
intent; clients carry `audio=1` in the player URL so the choice survives
next/previous, autoplay and a reload, exactly as the shuffle seed does.

## Backend ↔ TubeArchivist mapping

| Flimm | TubeArchivist |
|---|---|
| video list for feed/channel | `GET /api/video/?channel=&watch=&sort=&order=&type=&page=` (fan out per channel for feeds; merge + sort in backend; cache per user 30 s) |
| unseen counts | `GET /api/video/?channel=<id>&watch=unwatched&page_size=1` total hits, cached 60 s per channel |
| detail | `GET /api/video/{id}/`, `/nav/`, `/similar/`, `/comment/` |
| progress | `POST/DELETE /api/video/{id}/progress/` |
| watched | `POST /api/watched/ { id, is_watched }` (also accepts channel/playlist ids) |
| channels | `GET /api/channel/`, `/api/channel/{id}/`, `/aggs/`, `/nav/`, `/api/channel/search/?q=` |
| playlists | `GET /api/playlist/?type=custom\|regular&channel=`, `POST /api/playlist/custom/`, `POST /api/playlist/custom/{id}/`, `DELETE /api/playlist/{id}/` |
| search | `GET /api/search/?query=` (prefixes `video:`, `channel:`, `playlist:`, `full:` + `lang:`) |
| auth | header `Authorization: Token $TA_TOKEN` |
| media | `/media/…` (TA reports `media_url` as `/youtube/<channel>/<file>`; the `/youtube/` prefix maps to `/media/`), `/cache/videos/{id[0].lower()}/{id}.jpg`, `/cache/channels/{id}_thumb.jpg`, `_banner.jpg`, `/cache/playlists/{id}.jpg` — all gated by TA's nginx `auth_request /api/ping/`, which accepts the Token header |

The TA client lives in `internal/ta` with an interface so handlers can be
tested against a fake.

## Configuration (env)

| Var | Required | Notes |
|---|---|---|
| `TA_URL` | yes | e.g. `http://tubearchivist:8000` (in-cluster) or public URL |
| `TA_TOKEN` | yes | TA API token (Settings → user) |
| `DATABASE_URL` | yes | Postgres DSN |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID` | unless `AUTH_DISABLED=true` | |
| `ADMIN_EMAILS` | no | comma list; admins see `/healthz` details |
| `MEDIA_TOKEN_SECRET` | yes | HMAC secret for the media cookie |
| `PUBLIC_URL` | yes | for cookie/CORS |
| `MIN_PLAY_SECONDS` | no | seconds of playback before a video is recorded; default 15 |
| `MEDIA_CACHE_DIR` | no | where derived media is cached; default a temp dir |
| `MEDIA_CACHE_MAX_BYTES` | no | cache size cap before LRU eviction; default 5 GiB |
| `FFMPEG_PATH` | no | ffmpeg binary; default `ffmpeg` on `PATH` |
| `APP_NAME` | no | default `Flimm` |
| `PORT` | no | default 8080 |
| `SENTRY_DSN` | no | |
| `LOG_LEVEL` | no | |
