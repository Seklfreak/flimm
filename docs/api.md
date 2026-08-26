# Archive API contract (v1)

Archive is a client for a single [TubeArchivist](https://github.com/tubearchivist/tubearchivist)
instance. All clients (web, iOS, iPadOS, tvOS) talk **only** to the Archive backend;
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
  set headers, so media accepts **either** a Bearer header **or** an `archive_media`
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
Archive's `watch_events` table, falling back to TA's watched flag when Archive
has no event.

### Video (detail) — VideoSummary plus
```json
{
  "description": "…",
  "height": 1080,
  "media_url": "/media/video/yt-id.mp4",
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
  "resume_video_id": "yt-id" | null    // first in-progress, else first unseen
}
```

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
| POST | `/session/media` | sets `archive_media` cookie, 204 |

Prefs:
```json
{
  "autoplay": true,
  "playback_speed": 1.0,
  "subtitle_lang": "en" | null,        // null = off
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
| GET | `/videos/{id}/up-next` | query `feed=<id>` or `playlist=<id>` or `channel=<id>`; returns next VideoSummary[] (max 20) in that context, falling back to `similar` |
| GET | `/videos/{id}/similar` | VideoSummary[] (TA similar) |
| GET | `/videos/{id}/comments` | TA comments passthrough |
| POST | `/videos/{id}/progress` | `{ "position": 561 }` — heartbeat. Upserts watch_event; writes TA `/video/{id}/progress/`; at ≥90% (or ≤30 s remaining) marks watched. Returns `{ "position", "watched" }` |
| POST | `/videos/{id}/watched` | `{ "watched": true\|false }` — writes TA `/watched/`; true completes the watch_event, false clears position and TA progress |
| DELETE | `/videos/{id}/progress` | "Start over": position → 0, TA progress deleted, 204 |

### Playlists
| Method | Path | Notes |
|---|---|---|
| GET | `/playlists` | query `kind=custom\|channel`, paged PlaylistSummary; custom first |
| POST | `/playlists` | `{ "name" }` → TA `/playlist/custom/` (201) |
| GET | `/playlists/{id}` | PlaylistSummary + `items: [{ "position", "video": VideoSummary }]` |
| PATCH | `/playlists/{id}` | `{ "name" }` custom only (rename is client-side + TA re-create if TA has no rename; document limitation) |
| DELETE | `/playlists/{id}` | custom only; TA delete without videos; 204 |
| POST | `/playlists/{id}/videos` | `{ "video_id", "action": "add\|remove\|up\|down\|top\|bottom" }` → TA custom playlist actions |

### History
| Method | Path | Notes |
|---|---|---|
| GET | `/history` | query `filter=all\|in_progress\|seen`, `q` (title/channel substring), paged HistoryEntry, newest first |
| DELETE | `/history/{entry_id}` | hides the entry (soft delete), 204; does not change watched state |

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

## Backend ↔ TubeArchivist mapping

| Archive | TubeArchivist |
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
| `APP_NAME` | no | default `Archive` |
| `PORT` | no | default 8080 |
| `SENTRY_DSN` | no | |
| `LOG_LEVEL` | no | |
