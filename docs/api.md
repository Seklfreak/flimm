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
  set headers, so media accepts **any** of three: a Bearer header, an
  `flimm_media` cookie, or the same token as a `media_token` query parameter.
  `POST /api/v1/session/media` (authenticated) sets that HttpOnly, SameSite=Lax,
  Secure cookie and **returns the token in its body**
  (`{ "token": "…", "expires_in": 2592000 }`); the web app calls it after login
  and again when a media request returns 401, and ignores the body. Native
  clients pass the Bearer header via `AVURLAssetHTTPHeaderFieldsKey`.
- **A media token lasts 30 days** (`MEDIA_TOKEN_SECONDS`). It outlives the
  session that minted it on purpose: the Apple TV's top shelf holds URLs the
  *system* fetches days after the app last ran, and a viewer who does not open
  Flimm for a fortnight should not come back to a row of missing pictures. It
  is a bearer credential for `/media/*` and nothing else — no API, no account —
  signed, carrying its own user id and expiry. A month of validity is a month a
  leaked URL would work; shorten it where that is the wrong trade.
- **The query parameter is for a fetcher that can set neither.** tvOS draws the
  Home screen's top shelf itself, in a process with no session of ours, from
  URLs an app extension hands it — a URL that carries its own credential is the
  only way that artwork can be authenticated at all. It is the same 12-hour,
  media-only token, and it is **redacted from the access log** before anything
  formats the request line, because a credential in a log is a credential given
  away.
- OIDC discovery for clients: `GET /api/v1/config` (unauthenticated) returns
  `{ "app_name", "oidc_issuer", "oidc_client_id", "version", "auth_disabled",
  "analytics_disabled" }` so native apps need only the server URL.
- **`auth_disabled: true`** means the deployment runs with `AUTH_DISABLED=true`:
  there is no sign-in, and every request is the same fixed dev user. A client
  connects to such a server without an OIDC flow and sends any non-empty bearer
  token (the value is ignored, but `/media/*` still requires the header). The
  server says this outright rather than leaving it to be inferred from empty
  OIDC fields, because the two cases are opposites: a server deliberately
  running open is one to connect to, while a server that wants auth but
  publishes no issuer is broken and a client must refuse it.
- **`analytics_disabled: true`** means the deployment runs with
  `ANALYTICS_DISABLED=true`: no client may report usage to the analytics
  endpoint it was built with. Every client reads this — the web app before it
  loads the tracker, the Apple apps as soon as the server is known — and one
  built without an endpoint reports nothing either way. See the README's
  "Analytics".

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
  "dismissed": false,                  // taken out of the feeds without watching it
  "position": 561,                     // where to resume, in seconds, 0 if none
  "progress": 0.38,                    // how far the viewer got, 0 or 1 when watched
  "last_played_at": "2026-08-26T15:42:00Z"  // null if never played here
}
```

`position` is a **resume point, not a bookmark**: it is 15 seconds behind the
position last reported, so a viewer coming back lands a little before where
they stopped rather than mid-sentence. The server owns the rewind so every
client resumes alike and none implements it; the stored position (and what is
written back to TubeArchivist) is untouched, and `progress` is computed before
the rewind, so a card's bar still shows how far the viewer really got. A
watched video keeps its recorded position — it is started over, not resumed.

```
```
`watched`, `position`, `progress`, `last_played_at` are per-user and come from
Flimm's `watch_events` table, falling back to TA's watched flag when Flimm
has no event.

`dismissed` is a **separate decision from watching**: "I am not going to watch
this, take it out of my feeds." Feed listings (including *Everything*, and in
every view — unseen, in-progress, all) drop dismissed videos server-side, so no
client has to filter. *Up next* drops them too, so autoplay never
plays something the viewer has dismissed. Channel pages, playlists, search and
history still show them, with `dismissed: true` — that is where a viewer finds
one again and puts it back. It never implies anything about watch state, and it is
never written to TubeArchivist; marking a video seen to clear a feed would do
both, which is the whole reason this exists. It also has nothing to do with the
`hidden` flag on a history entry (`DELETE /history/{id}`), which only removes a
row from history and returns on the next play.

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
  "audio_url": "/media/audio/yt-id.webm",       // Opus in WebM; derived on first request, see Derived media
  "audio_aac_url": "/media/audio/yt-id.m4a",   // the same audio as AAC in MP4, for AVFoundation
  "hls_url": "/media/hls/yt-id/1080/master.m3u8", // the default compatible rendition; always present
  "hls_state": "pending|running|done|failed",  // where that rendition stands, see Derived media
  "hls_variants": [                            // every quality offered, tallest first
    { "height": 1080, "url": "/media/hls/yt-id/1080/master.m3u8", "state": "done",    "codec": "h264", "hls_progress": 1 },
    { "height": 720,  "url": "/media/hls/yt-id/720/master.m3u8",  "state": "running", "codec": "h264", "hls_progress": 0.37 },
    { "height": 480,  "url": "/media/hls/yt-id/480/master.m3u8",  "state": "pending", "codec": "h264", "hls_progress": 0 }
  ],
  "preview_url": "/media/preview/yt-id/preview.vtt", // scrub-preview stills, see Derived media
  "youtube_url": "https://www.youtube.com/watch?v=yt-id",
  "streams": [ { "type": "video", "codec": "avc1", "width": 1920, "height": 1080, "bitrate": 4500000 },
               { "type": "audio", "codec": "mp4a", "width": 0, "height": 0, "bitrate": 130000 } ],
  "subtitles": [ { "lang": "en", "source": "user|auto", "url": "/media/subtitles/yt-id/en.vtt" } ],
  "sponsorblock": [ { "category": "sponsor", "action_type": "skip", "start": 12.3, "end": 45.6 } ],
  "stats": { "views": 0, "likes": 0, "dislikes": 0 }, // see "Views and votes"; dislikes absent unless known
  "tags": [],
  "playlists": [ { "id": "PL…|custom-id", "name": "…", "position": 9, "count": 14 } ],
  "channel": { …ChannelSummary… }
}
```
`streams` mirrors TA's per-video source renditions as parsed from the video
document (never re-muxed by Flimm). Native clients use `codec` to decide
whether `media_url` is directly playable by AVFoundation: H.264 (`avc1`) video
with AAC (`mp4a`) audio always is; VP9 (`vp09`)/AV1 (`av01`) video or Opus
audio support is device-dependent. When it is not playable, `hls_url` is the
answer — see [Compatible video renditions (HLS)](#compatible-video-renditions-hls).

### SponsorBlock

`sponsorblock` is fetched **from the SponsorBlock service by the backend**, not
read out of TubeArchivist. TA stores a snapshot taken at download time with
whatever categories it was configured for; segments keep being submitted,
corrected and downvoted long afterwards, so Flimm asks the service and only
falls back to that snapshot when it has to (no `SPONSORBLOCK_URL`, or a lookup
that failed — an offline deploy, an outage, a timeout). An answer of "this
video has no segments" is authoritative and wins over the snapshot: a segment
that was removed must not come back.

The lookup never tells the service which video is playing. It sends the first
four hex characters of `sha256(video_id)`, gets every video sharing that prefix
back and picks ours out server-side. Answers are cached for hours, and a
failing service is remembered for ten minutes so an offline deploy costs one
timeout rather than one per request.

Segments are clamped to *this* copy of the video: one submitted against a
different cut that starts past the end is dropped, one that overruns it ends at
the duration.

`action_type` says what a client may do with a segment — a client that ignores
it would skip things nobody asked it to skip:

| `action_type` | means |
|---|---|
| `skip` | seek past it (the only one that may be skipped automatically) |
| `mute` | keep playing, muted, for its length — the picture still matters there |
| `poi` | a single point of interest ("the highlight"), where `start == end` — offered as a jump, never taken automatically |
| `full` | labels the whole video rather than a range of it |

It is `skip` on the snapshot path (TA stores no action) and absent only on a
server that predates the field, which is the same thing. `chapter` segments
never appear here — they come back from
[`/videos/{id}/chapters`](#chapters) instead.

What each *category* does is the viewer's, in `sponsor_actions` (below), and
every client obeys it identically: `skip` seeks past the segment, `ask` offers
a button in the player for as long as playback is inside it, and `off` leaves
it alone — still tinted on the timeline, since the tint says what a stretch of
video is, not what will happen to it. A `mute` segment is muted for anything
but `off`: the viewer asked to be offered the skip, not to hear the sponsor
read while they decide. `skip_sponsors` is the master switch above all of it.

A `poi` or `full` segment is never tinted: neither marks a stretch of the
timeline. The `poi` gets a marker and a *Jump to the highlight* control
instead, offered only while playback is still before it and regardless of any
preference — jumping is a click, not a skip.

`hls_url` is **always present**, whether or not the rendition exists yet;
`hls_state` says which:

| `hls_state` | means |
|---|---|
| `pending` | nobody has asked for it; the first request starts a transcode |
| `running` | being transcoded (or queued behind another transcode) |
| `done` | on disk; playback starts immediately |
| `failed` | the last attempt failed; the next request tries again |

`hls_variants` is the quality ladder, **tallest first**, and it is what a client
picks from. A video offers every height at or below its source height, out of
2160, 1440, 1080, 720 and 480 (a source whose height TA did not parse offers
1080 and below; a source shorter than 480 still offers that one rung). Each
entry is a rendition in its own right: its own URL, its own cache entry, its own
`state`, derived only when something asks for it — so the states within one
video differ.

| field | means |
|---|---|
| `height` | the rendition's height in pixels; the width follows the source's aspect ratio |
| `url` | the playlist to load, `/media/hls/{id}/{height}/master.m3u8` — a **multivariant (master) playlist** that names the codecs so hls.js schedules the fMP4 fragments; it references the media playlist at `index.m3u8` in the same directory, which stays directly reachable |
| `state` | `pending\|running\|done\|failed` for *that* height, exactly as `hls_state` |
| `codec` | `h264` (heights up to 1080) or `hevc` (1440 and 2160) — see [Compatible video renditions (HLS)](#compatible-video-renditions-hls) |
| `hls_progress` | how much of that rendition has been transcoded, `0`–`1`. `1` for a finished one, `0` for one nothing has asked for. It is **not** how far playback can get: the playlist is complete from the first request and segments are filled in wherever the viewer is, so this is only a number to show while preparing |

`hls_url` and `hls_state` stay for clients that do not choose: they are the
1080p entry, or the tallest one offered when the source is smaller than that.

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

`unseen_count` is TubeArchivist's unwatched total for the feed's channels. It
is a **hint, not the list's length**: TA knows nothing about the feed's own
filters (shorts, subtitles-only) or about videos the user dismissed, so it can
read higher than `GET /feeds/{id}/videos?view=unseen` reports as its `total`.

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
  "music": false                       // a music playlist: audio-only, and no watch state
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
return `{ "items": [...], "page": 0, "page_size": 30, "total": 123,
"has_more": true, "next_cursor": "…" }`.

**Page on `has_more`, never on `total`.** Video lists are composed lazily: the
server merges the feed's channels, applies its filters and the user's watch and
dismissal state, and stops as soon as it has one item past the window it was
asked for. It therefore knows how far it walked but not what lies behind, so
`total` is a **floor** while `has_more` is true, and exact only once it is
false. A client that compares `(page + 1) * page_size` against `total` ends the
list after its first page.

There is no ceiling on how large a list can be. Composition is bounded only by
`maxComposeVideos`, how many TubeArchivist rows a single request may walk while
filling its window — reached only when the filters reject nearly everything,
never by ordinary browsing.

**Follow `next_cursor`.** Feed and channel video lists also return an opaque
`next_cursor` alongside `has_more`; send it back as `?cursor=…` (instead of
`page`, not alongside it) and the server resumes exactly where the last page
stopped. Offset paging still works and still costs what it always did — `page=40`
makes the server walk the forty pages before it — so a client should ask for
`page=0` once and follow cursors from then on. A cursor belongs to the list
that issued it: sending one to a different list, or a different view of the
same list, is **400 `invalid cursor`**, because quietly serving page 0 instead
would show the reader everything twice. Restart the list from `page=0` if that
happens. Lists that are not composed lazily (playlists, history, channels,
search, up next, "continue watching") have no cursor and page by offset.

`unseen_count` on a feed is unrelated to any of this and can still read higher
than a list; see the Feed section.

Note that TA paginates at a size it chooses (12 by default) and ignores the
`page_size` Flimm sends, so a short page from TA never means the last one.

## Endpoints

### Meta / session
| Method | Path | Notes |
|---|---|---|
| GET | `/config` | unauthenticated; app name, OIDC issuer/client id, version |
| GET | `/healthz` | unauthenticated **readiness**: 200 when the DB answers, 503 when it does not. `ta` reports TubeArchivist as `ok`/`slow`/`unreachable` beside that verdict without deciding it — the check is time-boxed and cached, so a slow archive cannot make the probe late. An admin (`ADMIN_EMAILS`) also gets `stalls`: the recent [playback stalls](#playback-stalls) with the server's attribution |
| GET | `/livez` | unauthenticated **liveness**: 200 whenever the process is answering. Touches neither the DB nor TA — restarting fixes neither |
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
  "normalize_loudness": true,          // even out the volume between channels
  "skip_sponsors": true,               // the master switch for everything below
  "dearrow_titles": "off",             // "off" | "manual" | "all"
  "dearrow_thumbnails": "off",         // set independently of titles
  "sponsor_actions": {                 // per category: "skip" | "ask" | "off"
    "sponsor": "skip", "selfpromo": "skip", "interaction": "skip",
    "intro": "ask", "outro": "ask", "preview": "ask",
    "filler": "ask", "music_offtopic": "ask", "exclusive_access": "ask"
  },
  "everything_sort": "newest", "everything_hide_seen": true, "everything_include_shorts": false,
  "theme": "system|light|dark"
}
```

**DeArrow** (`dearrow_titles`, `dearrow_thumbnails`) is applied by the server,
so a video is called the same thing in every client and every list — feed,
search, history, up next and the player. `manual` uses what people submitted
and the crowd voted on; `all` also takes what DeArrow generates where nobody
has: a title with the shouting taken out, and the frame the service suggests.
A crowd that voted to *keep* the original is obeyed by both. The two are
separate preferences because they are separate things to want.

**Nobody waits for the same video twice.** What DeArrow said is kept in
`dearrow_branding` — one row per video, shared by every user, and durable, so a
restart does not throw it away (the cache before this one lived in memory and
died on every deploy). A row is served immediately however old it is; past its
freshness window a refresh is queued *behind* the response. Only a video with no
row at all is fetched inside the request, under a **2.5 s** deadline — the
service answers in 200–400 ms when healthy and has been measured at fifteen
seconds when not, and a page must not be able to wait that long. Past the
deadline the archive's own title goes out and the lookup finishes in the
background, so the second view is right.

Freshness is two windows because the two answers age differently: **24 h** for a
row carrying a submission (votes move), **7 days** for "nobody has submitted
anything" — which is around nine rows in ten, measured against a real archive,
and the least likely to change. A background sweep walks the archive every six
hours and queues whatever is missing or stale at a deliberate crawl, so a video
downloaded today is usually already known by the time anyone opens the page it
is on.

A crowd-sourced thumbnail comes back as a `thumb_url` of
`/media/frame/{id}/{ms}.jpg` — DeArrow returns a timestamp, not an image, so
the frame is cut from the deployment's own copy of the video (cached like any
other derived media) and no third party is asked for a picture. That endpoint
falls back to the archive's own thumbnail when a frame cannot be cut, and
nothing is looked up at all unless a viewer asked for it — see `DEARROW_URL`.

**Loudness normalisation** (`normalize_loudness`, on by default) is what makes
the player apply `gain_db`. It is on where DeArrow is off, because it asks
nobody anything, changes nothing about what a video *is*, and only ever turns a
video down.

`sponsor_actions` always comes back with **every** category the server knows,
so a client can tell "left alone" from "this build predates the category". A
`PATCH` replaces the map it is sent, filling any category the patch omits with
its default — send the whole map back, which is what the settings screens do.
`poi_highlight` is not in it: a point of interest is offered, never configured.

### Feeds
| Method | Path | Notes |
|---|---|---|
| GET | `/feeds` | all feeds incl. `everything`, ordered by `position`, with unseen counts |
| POST | `/feeds` | body: name, channel_ids, options → Feed (201) |
| GET | `/feeds/{id}` | |
| PUT | `/feeds/{id}` | full update; `pinned:true` unpins the others |
| DELETE | `/feeds/{id}` | 204; never touches channels/videos |
| POST | `/feeds/reorder` | `{ "ids": [...] }` |
| GET | `/feeds/{id}/videos` | query `view=unseen\|all` (default: feed's `hide_seen` → unseen else all), paged. **`view=unseen` opens with the videos the viewer is part-way through**, most recently played first, then the rest of the unseen feed; each appears once, and paging carries across the join. `view=continue` is accepted for clients built before that and answers with those in-progress videos alone |
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
| GET | `/videos/{id}/comments` | the archived comments, **paged by thread** — `Page<Comment>`, a comment's replies riding along with it. Normalised from what TubeArchivist indexed; see [Comments](#comments) |
| GET | `/videos/{id}/chapters` | chapter markers for the scrubber (see below); cached per video |
| GET | `/videos/{id}/loudness` | how loud the video is and the gain to play it at — `{ "state": "pending\|running\|done\|failed", "gain_db": -3.9, "target_lufs": -18, "measured_lufs": -14.1, "peak_dbtp": -3.8, "range_lu": 6.1 }`. The first call **starts the measurement** and answers `running` with a gain of 0; the numbers arrive on a later call. See [Loudness normalisation](#loudness-normalisation) |
| POST | `/videos/{id}/progress` | `{ "position": 561 }` — heartbeat. Upserts watch_event; writes TA `/video/{id}/progress/`; at ≥90% (or ≤30 s remaining) marks watched, and **un-marks it** when a seen video is being watched again (see below). Returns `{ "position", "watched" }`. **Nothing is recorded below `MIN_PLAY_SECONDS`** unless the video completes or an event already exists — see below | Pass `?playlist=<id>` so the server can skip recording for music playlists.
| POST | `/videos/{id}/stall` | `{ "position": 2472.5, "seconds": 3.1, "height": 1080, "client": "tvos" }` — the picture stopped mid-playback. 204, always: the report is fire-and-forget and never worth telling a viewer about. Anything under 0.4 s is dropped as the ordinary gap between segments. The **server** attributes it — see [Playback stalls](#playback-stalls) |
| POST | `/videos/{id}/watched` | `{ "watched": true\|false }` — writes TA `/watched/`; true completes the watch_event, false clears position and TA progress |
| DELETE | `/videos/{id}/progress` | "Start over": position → 0, TA progress deleted, 204 |
| POST | `/videos/{id}/dismiss` | take the video out of every feed without watching it; returns `{ "dismissed": true }`. Verified against TA first, so an unknown id is **404**. Idempotent, and the original dismissal time is kept |
| DELETE | `/videos/{id}/dismiss` | put it back; returns `{ "dismissed": false }`. Undoing something that was never dismissed is a success, so an undo control cannot fail on a double tap |
| POST | `/videos/{id}/hls` | starts (or re-aims) a compatible video rendition **without waiting** and returns `{ "state": "pending\|running\|done\|failed", "height": 1080, "hls_progress": 0.37 }`, so a client can prefetch instead of making the viewer wait at play time. `?height=` picks which rendition; without it the one `hls_url` points at. A height the video does not offer (not in `hls_variants`) is **400**. `?from=<seconds>` is the **resume position**: the transcode starts at that point instead of at 0:00, and a job that is already running is re-aimed at it — send it before handing the playlist to a player and again after a seek. A `from` that is not a position inside the video is ignored. Idempotent: a running or finished rendition is not started again |

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

#### Music playlists

A playlist marked `music` is a different kind of thing from a video playlist,
and the API treats it as one rather than leaving each client to paper over it:

- **No watch state is recorded.** The progress heartbeat takes the playlist it
  is playing from (`?playlist=<id>`); when that playlist is music, nothing is
  written — no watch event, no watched flag, no history entry, and so nothing
  in "continue watching". A song is replayed, so "seen" means nothing, and
  recording it would fill history with tracks.
- **No watch state is reported.** `seen_count`, `in_progress_count`,
  `progress` and `resume_video_id` come back zeroed, and the playlist's videos
  carry no `watched`, `position` or `last_played_at`. Clients therefore render
  no seen ticks, progress bars or resume chips without special-casing music.
- **Playback is audio-only**: clients use `audio_url` (`audio_aac_url` on
  Apple platforms, see [Derived media](#derived-media)) and carry `audio=1`.

The flag is per user and per playlist, and the same video played from
somewhere else is ordinary viewing again — watch state is only suppressed for
playback *from* the music playlist.

#### Chapters

```json
{
  "source": "embedded|sponsorblock|description|none",
  "chapters": [ { "start": 0, "end": 132.5, "title": "Intro" } ]
}
```

TubeArchivist stores no chapters, so Flimm derives them, preferring the
authoritative source:

1. **`embedded`** — yt-dlp embeds YouTube's chapters into the container at
   download time. The backend range-fetches the `moov` box (files are
   faststart, so it sits at the front) and reads the Nero `chpl` box, falling
   back to the QuickTime chapter text track referenced by `tref`/`chap`.
2. **`sponsorblock`** — chapter names submitted to SponsorBlock (its `chapter`
   action type), through the same hash-prefix lookup as the segments. Used when
   the file carries none of its own: hand-written names beat the description
   heuristic.
3. **`description`** — timestamp lines in the description (`0:00 Intro`,
   `1:02:03 - Something`). Used only when nothing is embedded and nobody has
   submitted chapters, and only when at least two timestamps parse and they
   increase monotonically.
4. **`none`** — `chapters` is `[]`.

`end` is the next chapter's `start`, and the last chapter ends at the video
duration. Times are seconds (float). Titles are trimmed and never empty.
Clients treat an empty list as "no chapter UI", never as an error.

### Playlists
| Method | Path | Notes |
|---|---|---|
| GET | `/playlists/pinned` | PlaylistSummary[] the user pinned to the sidebar, in `position` order; unpaged |
| PUT | `/playlists/{id}/pinned` | `{ "pinned": true\|false }` → 204. Pinning appends to the end; unpinning closes the gap |
| PUT | `/playlists/{id}/music` | `{ "music": true\|false }` → 204. Marks the playlist as music: audio-only playback and no watch state (see below) |
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
| GET | `/stats` | what that history adds up to: `{ "started", "finished", "seconds", "since", "range", "zone", "top_channels": [{ "id", "name", "videos", "seconds" }], "by_hour": [24], "by_weekday": [7], "by_month": [{ "month": "2026-08", "videos", "seconds" }] }`. `?range=all\|year\|month` (calendar windows, default all; anything else **400**), `?tz=` an IANA zone the breakdowns are computed in (default UTC). See [Watch stats](#watch-stats) for what these numbers can honestly say |
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

**Watching a seen video again un-seens it.** A heartbeat that is not itself a
completion, past the same `MIN_PLAY_SECONDS`, clears the completion — in the
database and in TubeArchivist, whose own UI reads that flag. Without it a video
finished once could never hold a resume position again: every client reads
`watched` and starts from zero, so the position recorded underneath was never
used, and half an hour into a second viewing it still began at 0:00. The
threshold is what keeps opening something by accident from undoing having seen
it. Finishing it a second time keeps the *first* completion's timestamp, so
history does not claim it was first finished today.

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
| `GET /media/audio/{id}.webm` | audio-only stream, Opus in WebM, derived and cached on first request (see below); supports `Range` |
| `GET /media/audio/{id}.m4a` | the same audio as AAC in MP4 (`audio/mp4`), for players that cannot decode Opus in WebM; derived and cached the same way; supports `Range` |
| `GET /media/hls/{id}/{height}/master.m3u8` | the compatible rendition's **multivariant (master) playlist** (`application/vnd.apple.mpegurl`) at that height — the URL `hls_url` and `hls_variants[].url` point at. It is a one-entry master carrying an `#EXT-X-STREAM-INF` with `BANDWIDTH`, `CODECS` (e.g. `avc1.640829,mp4a.40.2`), `CLOSED-CAPTIONS=NONE` (the renditions carry no embedded captions; saying so is what stops `AVPlayer` inventing a "CC" subtitle option) and, when known, `RESOLUTION`, and a single relative variant URI of `index.m3u8`. It exists because **hls.js** will not schedule fMP4 fragments from a codec-less media playlist; a master naming the codecs makes it, and native `AVPlayer`/Safari take it just as happily. `{height}` must be one of 2160, 1440, 1080, 720, 480 and one the video offers (`hls_variants`), else **404**. It starts the transcode like the media-playlist route. `?from=<seconds>` is carried through to the variant URI (`index.m3u8?from=<seconds>`), so a player following the master lands on the media playlist that carries the matching `#EXT-X-START`; a `from`-specific master is served `no-store`. The `CODECS` string is parsed from the init segment the job produces (truthful even for a copied source); before the first init lands it is the height's fixed-encoder default (served `no-store` until the real one is known) |
| `GET /media/hls/{id}/{height}/index.m3u8` | the compatible rendition's **media** playlist (`application/vnd.apple.mpegurl`) at that height — what the master references, and what native players and the byte-range path load directly. Same `{height}` rules and **404**. Starts the transcode on the first request and returns the **complete VOD playlist immediately** — every segment of the video, `#EXT-X-ENDLIST` on the end — whatever the encoder has reached. `?from=<seconds>` (inside `(0, duration)`) is the resume position: it starts the transcode there **and** adds an `#EXT-X-START:TIME-OFFSET=<seconds>,PRECISE=YES` to the playlist header, so a resuming player begins at that point and fetches the resume segment first instead of blocking on segment 0 — which the resume-first transcode produces last. The segment list is unchanged (still the whole video, seekable anywhere); a `from` outside the range adds no tag. A `from`-specific playlist is served `no-store` (it is per-`from`; never cached as the canonical no-`from` playlist). 503 + `Retry-After: 5` only if the playlist itself cannot be produced (a video whose duration has to be probed from an unreachable source) |
| `GET /media/hls/{id}/{height}/init.mp4` | the fMP4 initialisation segment (`video/mp4`) |
| `GET /media/hls/{id}/{height}/seg00000.m4s` | a media segment (`video/iso.segment`). A segment the encoder has not reached **blocks** until it lands, up to `MEDIA_SEGMENT_WAIT` (60 s), then 503 + `Retry-After: 2`; a request far ahead of the encoder — or one behind it that this run has skipped — re-aims it. 404 for a segment past the end of the video or of a rendition nothing is deriving; 502 if the transcode failed |
| `GET /media/hls/{id}/{height}/iframe.m3u8` | the rendition's **I-frame playlist** (`application/vnd.apple.mpegurl`): one `#EXT-X-BYTERANGE` per segment, covering the keyframe each one opens with, so a player has pictures to show while scrubbing. **404 until the rendition is complete** — a playlist built mid-transcode would cover only the part encoded so far, and a player holds it for the whole session. The master carries `#EXT-X-I-FRAME-STREAM-INF` pointing at it under the same condition. Nothing is encoded for it and no job is started by it; see [Scrubbing](#scrubbing) |
| `GET /media/hls/{id}/master.m3u8`, `/index.m3u8`, `/init.mp4`, `/seg00000.m4s` | the same files without a height: a **legacy alias** for the 1080p rendition, kept for clients written before the ladder. It serves that rendition's cache entry rather than one of its own. New clients use `hls_variants` |
| `GET /media/subtitles/{id}/{lang}.vtt` | TA subtitle track |
| `GET /media/frame/{id}/{ms}.jpg` | derived: one still, cut on first request and cached — what a DeArrow thumbnail resolves to |
| `GET /media/preview/{id}/preview.vtt` | derived: the scrub-preview track (`text/vtt`), one cue per still, each pointing at its rectangle of the sheet as `sheet.jpg#xywh=x,y,w,h`. The first request **starts the derivation** and answers **404** (`Cache-Control: no-store`) until both files are on disk; a client scrubs without pictures and asks again |
| `GET /media/preview/{id}/sheet.jpg` | derived: the sprite sheet those cues cut from (`image/jpeg`). Same 404-until-ready rule. Both are `private, max-age=86400, immutable` once served |
| `GET /media/thumb/video/{id}` | TA `/cache/videos/…` |
| `GET /media/thumb/channel/{id}` and `/media/thumb/channel/{id}/banner` | TA `/cache/channels/…` |
| `GET /media/thumb/playlist/{id}` | TA `/cache/playlists/…` |

Thumbnails are cacheable (`Cache-Control: private, max-age=86400`).

The proxy rewrites `Content-Type` from the file extension when TA returns
`application/octet-stream`, and sets `Accept-Ranges: bytes` on 200/206. TA's
nginx declares a `types { text/vtt vtt; }` block on `/media/`, which replaces
the default MIME map for that location, so `.mp4` would otherwise arrive as
`application/octet-stream` and `<video>` refuses to decode it.


### Scrubbing

Dragging a scrubber over an HLS stream shows pictures only if the stream says
where its I-frames are. On the archived file a player can go and find them
itself, which is why the web and the phone have previews there and the Apple TV
scrubs a direct play happily. On the **compatible-rendition** path it cannot:
what is playing is a transcode it never sees whole, so without help the Apple
TV's scrubber is a bare timeline.

`GET /media/hls/{id}/{height}/iframe.m3u8` is that help, and it costs nothing to
make. The rendition is already cut on a keyframe grid, so every segment *begins*
with an I-frame; all a player needs is the byte range holding it. That range is
read out of the fragment itself — the `moof` box, its `trun`'s data offset, and
the first sample's size — so the playlist is one small read per segment and no
CPU at all. No second encode, no extra cache entry.

It is built from the rendition's own `index.m3u8` rather than recomputed, so
the two lists cannot disagree about segment names or the short last one. A
segment whose first sample cannot be located is left out rather than guessed
at: a wrong byte range is a broken picture, a missing one only a coarser scrub.

**Only for a finished rendition**, and it starts nothing. A player reads the
master once, so a video opened mid-transcode simply scrubs the way it always
did; the next time it is played the master carries the trick-play line.

Web clients are unaffected — hls.js parses the extra tag and ignores it — and
the archived-file path still uses the [preview sprite sheets](#derived-media),
which are a different mechanism for a different player.

### Watch stats

`GET /stats` is the whole of a viewer's history read sideways. Everything in it
comes from `watch_events`, which holds **one row per video per viewer** — the
furthest point reached, whether it completed, and when it was first and last
played. That shape decides what can honestly be said:

- **`seconds` is the summed furthest point reached.** A finished video counts
  its whole duration; an abandoned one counts where it stopped. A video watched
  three times counts once, and one skimmed through counts the part that was
  skipped past. It is a **floor** on time spent, not a stopwatch.
- **`by_hour` and `by_weekday` are when videos were *first started*.** That is
  the only moment the table records exactly. Someone who starts at midnight and
  watches until two appears at midnight, once.
- **`top_channels` is ordered by those same seconds**, and names at most eight.

None of that is a limitation to hide. Every client prints it under the numbers,
because a figure nobody qualifies is a figure people trust — and an invented
one here would look exactly like a real one.

**`tz` decides which evening a late-night play belongs to.** The server does not
guess: without it everything is computed in UTC, which is wrong for almost
everyone. Clients send their own zone (`Intl.DateTimeFormat()` on the web,
`TimeZone.current` on Apple) and it is echoed back as `zone` so the screen can
name it. `range=year` and `range=month` are **calendar** windows in that zone —
"this year" means the year on the wall, not the last 365 days.

**Clients.** A Stats page on the web (in the sidebar, next to History), a Stats
screen behind History on iPhone and iPad, and the headline numbers in Settings
on the Apple TV — no charts there, because a remote has nothing to hover over a
bar with, and the screen says where the charts are.

### Views and votes

`stats` on a video carries `views` and `likes` from TubeArchivist, which
indexes what YouTube publishes. Since 2021 that no longer includes the dislike
count, so `dislikes` has two possible sources:

1. **The archive itself.** TubeArchivist asks
   [Return YouTube Dislike](https://returnyoutubedislike.com) at index time when
   its own `integrate_ryd` setting is on, and stores the answer on the video. No
   deployment configuration is needed to show it, nothing is asked of anyone at
   view time, and it refreshes whenever TubeArchivist reindexes the video —
   along with the views, likes and comments beside it.
2. **A live lookup**, when `RYD_URL` is set. Newer than the archived number, at
   the cost described below, and it wins where both exist.

A zero from the archive is dropped rather than reported: TubeArchivist stores 0
both for a video with no dislikes and for one indexed while `integrate_ryd` was
off, and those are not the same claim. A live lookup can still supply a true
zero, because it says separately whether it knows the video at all.

Absent, not zero, is the rule throughout. A video nobody has counted and a video
with no dislikes are different facts, and a client that cannot tell them apart
draws "0 dislikes" over the first one. So the key is omitted whenever the answer
is unknown.

**When it answers, both vote counts come from it.** Its numbers are estimates
of the same kind measured against each other; pairing its dislike count with
the archive's like count from download day would put two vintages either side
of a slash and invite arithmetic on them. The one exception is a record with no
likes at all beside an archive that counted plenty — that is the service
missing data, not the video losing its likes, so the archive's like count
stands.

`views` is the **larger** of the archive's count and the service's, which is
the only comparison either number supports: a view count only goes up, so the
bigger one is simply the more recently read. The archive's was true the day the
file was downloaded and the service's the day it last looked, and neither knows
which of those was later. With no service configured it is the archive's, and
it is the one count here that every video has.

The lookup runs concurrently with the SponsorBlock one on `GET /videos/{id}`,
so it adds no latency of its own, answers are cached for six hours, and a
failure is not retried for two minutes.

**Clients** show `1.2M views · 45.1K 👍 · 1.2K 👎` under the title (web,
iPhone, iPad) or beside it in the Info panel (Apple TV), as counts rather than
controls — nothing here can vote on YouTube's behalf. Each part is drawn only
when it is there: the dislike count when the service supplied one, the like
count unless the archive recorded none — no thumb rather than a thumb reading
zero, and a *real* zero arrives with a dislike count beside it. So switching
`RYD_URL` on or off changes what every client shows with no client change at
all.

### Playback stalls

`POST /videos/{id}/stall` is how "why does it keep buffering?" becomes a
question with an answer.

Neither side can answer it alone. A client is the only one that knows the
picture stopped: no request fails, nothing errors, the viewer simply watches a
spinner. The server is the only one that knows *why* it might have, because it
knows where the encoder had got to and whether the segment being waited for
existed yet. So the client says what it was playing and for how long it stopped,
and the server attributes it — the same division as everywhere else here, with
the decision on the side that has the facts.

The attribution is deliberately a claim about **the segment**, not a guess about
the network: either the bytes existed when the viewer wanted them or they did
not, and that single fact says which half of the system to go and look at.

| `reason` | What it means |
| --- | --- |
| `encoder_behind` | the segment did not exist yet — the transcode is behind the viewer. The one cause the server can fix (`MEDIA_SEEK_AHEAD_SEGMENTS`, `MEDIA_TRANSCODE_JOBS`, a slower rung) |
| `delivery` | the segment existed — either the run had produced it, or, for a rendition finished before this playback began, the file is on disk. Whatever took the time was between the disk and the screen: the network, the client's buffer, the decoder |
| `source` | `height` was 0, so the archived file is being played directly and no rendition is involved — TubeArchivist or the network served it |
| `unknown` | nothing left to ask — no run, and no segment on disk either, usually because the rendition was evicted since |

Each stall is logged as `playback stalled` with its reason, position, segment,
encoder position and client, and the last 50 are shown to an admin on
`/healthz` under `stalls` — enough to see a pattern in an evening's watching.
Nothing is written to the database: this is an operational signal, not history.

**Clients.** Web (`useStallReport`), iPhone/iPad and Apple TV (FlimmKit's
`StallReporter`, via `PlaybackServices`). All four use the same 0.4 s floor,
and all four abandon rather than report a stall that was still running when
playback stopped — its length is unknown, and the viewer may simply have left.
### Derived media

TubeArchivist stores one file per video, muxed. Anything else a client needs —
two audio renditions and the compatible video renditions — is *derived* from
that file and cached on disk, keyed by `(video id, variant)`. An audio variant
is one file; each HLS variant is a directory (a playlist, an init segment and
the media segments), and there is one per offered height: `hls-1080`,
`hls-720` and so on.

There are two audio variants. They carry the same audio; they differ only in
what can decode them, so a client picks one and never both. The video variants
are described under
[Compatible video renditions (HLS)](#compatible-video-renditions-hls) below,
and the scrub-preview stills under [Scrub previews](#scrub-previews).

- `GET /media/audio/{id}.webm` is the `audio` variant. The archived audio is
  already Opus, so it is **remuxed, not re-encoded** (`-vn -c:a copy`): no
  quality loss, negligible CPU, and roughly 20–30× less data than the source
  (a 40-minute 1080p video is ~1.2 GB muxed and ~37 MB as audio). Browsers
  play it; this is what the web client uses.
- `GET /media/audio/{id}.m4a` is the `audio-aac` variant, AAC in MP4
  (`audio/mp4`). It exists because AVFoundation cannot decode Opus in WebM at
  all, so the native clients use `audio_aac_url` — for music playlists and as
  the fallback when a video codec is unplayable. Unless the source track is
  already AAC (`mp4a`, per `streams`), in which case it is copied like the
  WebM variant, this is a **real re-encode** (`-vn -c:a aac -b:a 128k
  -movflags +faststart`): it costs CPU and the first listener waits for the
  whole file, roughly a tenth of the video's duration on a modern core.
  Afterwards it is cached like any other variant and costs nothing.
  `+faststart` puts the MP4 index up front so byte-range seeking works.
- The first request for a variant produces it; concurrent requests for the same
  variant wait on that one job rather than each starting their own.
- Once produced, the file is served from disk with full `Range` support, so
  seeking and resume behave exactly like the video stream.
- The cache is bounded by `MEDIA_CACHE_MAX_BYTES` and evicted least-recently-
  used. A directory entry counts as the sum of its files, is touched whenever
  any file in it is served (so a rendition being watched is not evicted out
  from under the player) and is removed whole — half a rendition is worse than
  none. A directory whose transcode is still running is never evicted. It is a
  cache in the strict sense: deleting it costs only the CPU to re-derive, so it
  can live on ephemeral storage.
- Derivation reads the source from TubeArchivist over HTTP with the API token.
  The audio variants are piped into ffmpeg on stdin. The HLS variants need a
  **seekable** input — `-ss` on a pipe decodes and discards everything before
  the seek point, which is the whole cost resuming is meant to avoid — so they
  read through a loopback HTTP source instead: the server listens on
  `127.0.0.1` on an ephemeral port and serves the file, with `Range`,
  `Content-Length` and `Accept-Ranges` passed through, at `/src/<nonce>` where
  the nonce is a 128-bit random token minted per job and invalid the moment the
  job ends. ffmpeg is given only that URL, so the token still never reaches a
  command line, a child environment or a log line. Nothing is written back to
  TA.

Clients choose the stream. `audio_only` on a playlist is the persisted
intent; clients carry `audio=1` in the player URL so the choice survives
next/previous, autoplay and a reload, exactly as the shuffle seed does.

#### Comments

`GET /videos/{id}/comments` returns the comments TubeArchivist downloaded with
the video. It used to hand upstream's own JSON straight through —
`comment_likecount`, `comment_time_text` and all — which made every client a
second parser of somebody else's shape; it is a contract now, like everything
else here.

```json
{
  "id": "c1",
  "author": "@someone",
  "author_id": "UC-someone",
  "text": "Worth the wait.",
  "likes": 128,
  "published": "2026-08-20T09:00:00Z",  // null on an archive that kept only time_text
  "time_text": "1 week ago",            // upstream's own wording, the fallback
  "hearted": true,                      // by the uploader
  "from_uploader": false,               // written by the video's own channel
  "replies": [ …Comment… ]
}
```

**Threads page; replies do not.** A reply on its own says nothing, and there
are few enough per comment that paging them would cost more requests than it
saves bytes — so `page`/`page_size` walk the top-level comments and each one
carries its replies whole. `total` is exact here, unlike the lazily composed
lists: the whole tree is in hand before the window is cut.

A record with neither text nor author is **dropped**, because a half-indexed
row is not a comment and a blank line in a list is worse than a missing one.

**There is no author avatar, deliberately.** The archive holds a Google CDN URL
for it, and a client loading that would announce every video its viewer opens
to a third party — which is the one thing showing *archived* comments otherwise
avoids entirely. Clients draw the author's initial instead.

#### Loudness normalisation

`GET /videos/{id}/loudness` measures a video once (ffmpeg's `loudnorm` in its
measure-only mode, EBU R128) and says how many decibels to move it by. The
`loudness` variant is a directory holding that one small JSON file; nothing is
re-encoded and the archived file is never touched. `-vn` makes the pass cheap:
the whole file is read either way, but only the audio is decoded.

**The server computes the gain, not the clients.** It is the smaller of two
limits — the distance to the **-18 LUFS** target, and the headroom to a -1.0
dBTP ceiling — clamped so it is never a boost and never more than 15 dB of
attenuation. Four clients each deciding a level is four ways for the same video
to sound different on the TV and the phone.

**It only ever turns a video down**, and that is a platform fact rather than a
preference: `AVPlayer`'s volume tops out at 1.0 and an `AVAudioMix` does not
apply to an HLS stream, so the Apple clients cannot amplify at all — a web
client that could would then be louder than the TV playing the same video.
Attenuating alone still removes the jump between a loud channel and a quiet
one (YouTube attenuates above roughly -14 LUFS and boosts nothing, so an
archive sits mostly between -14 and -25), and it can never clip.

Clients ask once playback has begun, apply `gain_db` to the player's own volume
— under the system volume, not instead of it — and leave a video alone while
the measurement is running or when it failed. `normalize_loudness` is the
preference that turns the whole thing off.

#### Scrub previews

`preview_url` on the video detail is a WebVTT track of stills — the picture a
player shows above the scrubber while it is being dragged. The `preview`
variant is a directory holding two files: `sheet.jpg`, a sprite sheet of small
stills, and `preview.vtt`, one cue per still pointing at its rectangle of that
sheet (`sheet.jpg#xywh=x,y,w,h`, the fragment convention every player that
does this speaks). A sheet rather than a thousand files because a drag moves
through dozens of positions a second, and one image already in the client's
memory answers all of them.

The stills are 160px wide, ten to a row, at most 200 of them and never closer
together than two seconds — so a three-minute video gets one every two seconds
and a two-hour one gets one every 36, both in a sheet of a few hundred KB. The
track is written **after** the sheet, so its presence is what "ready" means:
a job that died halfway leaves a sheet no client can be handed.

It is the most expensive derivation per unit of use — a full decode of the
file, because stills at a *regular* interval are not the keyframes the encoder
happened to leave — so nothing derives it until a player asks, and the clients
only ask once playback has actually begun. Asking is what starts it: the first
request is a 404 and the answer arrives on a later one. Nothing waits on it;
a scrubber with no stills is still a scrubber.

#### Compatible video renditions (HLS)

`GET /media/hls/{id}/{height}/master.m3u8` is an `hls-<height>` variant: the
video transcoded to a codec Apple hardware decodes, with AAC audio, delivered
as HLS with fMP4 segments. It exists because the archive is full of AV1 and
VP9, which AVFoundation cannot decode on most Apple hardware — the source file
is simply unplayable there, and audio-only is a poor consolation.

The URL a client loads is a **multivariant (master) playlist**. A media
playlist with fMP4 segments and no `CODECS` attribute is one hls.js (the player
every non-Safari browser uses) parses and then stalls on — it never schedules
fragment 0, because it cannot create the MSE `SourceBuffer` without knowing the
codecs. A one-entry master carries `CODECS` in its `EXT-X-STREAM-INF`, which
fixes the stall; native `AVPlayer` and Safari play a master just as happily. The
master references the media playlist below it at `index.m3u8`, which stays
directly reachable for the byte-range and native paths. The `CODECS` string is
parsed from the init segment the transcode produces — `avc1.PPCCLL` from the
avcC record (High@4.1 → `avc1.640829`), `hvc1.…` from hvcC, plus `mp4a.40.2` —
so it is truthful even when the source is copied rather than re-encoded.

When the rendition is complete it also carries an
`#EXT-X-I-FRAME-STREAM-INF` pointing at `iframe.m3u8`, which is what a player
scrubs with — see [Scrubbing](#scrubbing).

It also carries `CLOSED-CAPTIONS=NONE`, which is not decoration. A stream that
says nothing about captions is one a player must assume *might* carry CEA-608/708
inside the video, and `AVPlayer` duly publishes a legible media-selection option
called "CC" for it — which on the Apple TV becomes a Subtitles entry in the
transport bar's own menu that selects captions which do not exist. The
renditions never carry embedded captions, so the playlist says so.

**The quality is the client's choice**, from the `hls_variants` ladder on the
video detail. Each height is derived on its own, on demand; asking for 720p on
a 4K video transcodes 720p and nothing else. The heights and their codecs:

| height | codec | encoder (GPU / CPU) | notes |
|---|---|---|---|
| 2160 | HEVC | `hevc_vaapi` (CQP 25) / `libx265` (crf 26) | Main, 8-bit 4:2:0, `hvc1` |
| 1440 | HEVC | `hevc_vaapi` (CQP 25) / `libx265` (crf 26) | Main, 8-bit 4:2:0, `hvc1` |
| 1080 | H.264 | `h264_vaapi` (CQP 23) / `libx264` (crf 23) | High@4.1, 4:2:0 |
| 720 | H.264 | `h264_vaapi` (CQP 23) / `libx264` (crf 23) | High@4.1, 4:2:0 |
| 480 | H.264 | `h264_vaapi` (CQP 23) / `libx264` (crf 23) | High@4.1, 4:2:0 |

**Why HEVC above 1080p.** An H.264 encode of 4K is enormous for the picture it
delivers, and 4K H.264 is beyond what Apple's decoders are specified for
anyway. HEVC halves the bitrate at the same quality, and **every Apple device
since the iPhone 7 and the first Apple TV 4K decodes it in hardware** — which
is everything that can drive a 4K panel in the first place. A client that
cannot decode HEVC (an old device, a browser without HEVC support) picks 1080
or below, which is what `codec` on each variant is for. HEVC tracks carry the
`hvc1` sample entry, not `hev1`: AVFoundation refuses the other one in fMP4.

**The client also decides when to use HLS at all.** Read `streams` from the
video detail: if a video stream's `codec` is one the device decodes (`avc1`
always; `vp09` and `av01` are device-dependent), play `media_url` directly — it
is the original file and costs the server nothing. Only when nothing is
playable should the client load a rendition. Never use one as the default: it
is a real transcode of someone's CPU.

**The playlist is complete from the first request, and the transcode starts
where the viewer is.** The segment grid is fixed — 4-second segments aligned to
the timeline — so the whole playlist follows from the video's duration and is
written before anything is encoded: `EXT-X-PLAYLIST-TYPE:VOD`, every segment
listed, `#EXT-X-ENDLIST` on the end. A player may therefore **seek anywhere in
the rendition immediately**, which is what makes resuming work: without it a
viewer resuming at 40:00 started at 0:00, because a player cannot seek past the
end of the playlist it holds and the playlist only reached 40:00 once the
encoder did.

- **Pass `from`.** `POST /videos/{id}/hls?height=<h>&from=<seconds>` (or
  `?from=` on the playlist/master URL handed to the player) is the resume
  position. The first ffmpeg run encodes from the segment that position falls in
  to the end of the video, and a second run fills in the part before it. Without
  `from` it is one run from the start, as before.
- **`from` also starts the *player* there, via `#EXT-X-START`.** Passing
  `?from=<seconds>` on the **playlist/master URL** (not only the `POST`) adds an
  `#EXT-X-START:TIME-OFFSET=<seconds>,PRECISE=YES` to the media playlist, and the
  master carries the query through to its `index.m3u8?from=<seconds>` variant
  URI. This is what makes resume *instant*: every HLS player (hls.js and
  `AVPlayer`) fetches segment 0 first to lay out the timeline before honouring a
  seek, but the resume-first transcode produces segment 0 **last** — so without
  the tag the player blocks on a segment that will not exist for a long time.
  `#EXT-X-START` moves the player's start point to the resume position, so the
  first segment it asks for is the one the transcode produces first. The playlist
  body is otherwise unchanged (still the complete VOD list, seekable anywhere); a
  `from` outside `(0, duration)` adds no tag. A client should therefore pass
  `?from=<resume position>` on the URL it hands the player, and `?from=<current
  time>` on a quality switch, not only in the `POST`.
- **A segment that is not encoded yet is a slow segment, not a missing one.**
  The request blocks until it lands, up to `MEDIA_SEGMENT_WAIT` (60 s), then
  answers **503 with `Retry-After: 2`**. Only a segment past the end of the
  video is a 404; a failed transcode is a 502.
- **A seek re-aims the encoder.** A segment request more than
  `MEDIA_SEEK_AHEAD_SEGMENTS` (30, about two minutes) ahead of where the run
  has got to cancels it and restarts from the requested segment, at most once
  every 10 s. So does a request for an unproduced segment **behind** the run —
  everything before a resume point, which the first run leaves for later. That
  one is not an optimisation: the current run will never write those segments,
  so waiting means waiting for the rest of the video to encode and the job to
  wrap around, which is minutes on a long video and forever from the point of
  view of a player that cancels a request after four seconds (`AVPlayer`
  does). Everything already produced stays; the job then fills the
  remaining gaps, taking the one holding the most recently requested segment
  first and the earliest gap after that, and is finished when all segments
  exist. Sending `from` again after a seek does the same thing without waiting
  for a segment request to trigger it.
- **Caching.** A running rendition's playlist is served `no-store`, because it
  is rewritten once at the end with the segments' real durations; a finished one
  gets a long cache lifetime. A playlist or master carrying a `?from=` is always
  `no-store` — it is per-`from` (the `#EXT-X-START` and the variant query depend
  on it) and must never be cached as the canonical no-`from` response. Segments
  are immutable and cached for a day.
- **Progress.** `hls_variants[].hls_progress` (and the `POST` response) is the
  fraction of the whole rendition that exists, for an honest "preparing…"
  label. It says nothing about *where* those segments are.

Costs, so nobody is surprised:

- **CPU.** Software AV1/VP9 decode plus an x264 (or x265) encode is CPU-bound
  and runs at roughly realtime per core on a modern server, so a 40-minute
  video takes tens of minutes to finish — though watching can start almost
  immediately. `MEDIA_TRANSCODE_JOBS` (default 1) caps how many run at once
  **across every video and every height**; extra requests queue, because two
  transcodes sharing a core make both viewers wait longer. A client that
  prefetches several qualities of the same video therefore serialises them —
  ask for the one that will be played.
- **Unless there is a GPU.** With an Intel iGPU exposed to the server
  (`MEDIA_HWACCEL`, default `auto`) the decode, the scale and the encode all
  run on it, and the same job takes minutes instead. Nothing about the
  renditions or this contract changes: same codecs, same profiles, same
  playlist, same states. A source the hardware decoder cannot take falls back
  to the software encode by itself, so a client never sees the difference
  except in how long it waits. See
  [deploy.md](deploy.md#hardware-acceleration-intel-vaapi).
- **Disk.** Roughly 1.5 GB for a 1080p hour and 1.5–2 GB for a 2160p HEVC hour (measured at the default quality settings),
  against `MEDIA_CACHE_MAX_BYTES` — and each height a client asks for is
  another entry. A few renditions fill the default 5 GiB cap, and the least
  recently watched are evicted.
- **Not always.** When the source already *is* the rendition — the same height
  in that height's codec (H.264 up to 1080p, HEVC above it) — the video track
  is **copied** (`-c:v copy`), and AAC audio is copied too; the job is then a
  segmentation, not a transcode, and is nearly free. The height must match
  exactly: a 1080p source is not the 720p rendition. The codecs come from TA's
  `streams` metadata, and a copy the muxer refuses falls back to a full encode
  rather than failing the request. A copy always runs over the whole video in
  one pass and ignores `from`: a stream copy can only cut on the source's own
  keyframes, not on the 4-second grid, and at remux speed the whole file is
  done in the time an encode needs for the first minute.

`hls_variants[].state` (and `hls_state` for the default height) reports
`pending|running|done|failed` so a client can show "preparing…" honestly, with
`hls_progress` next to it, and `POST /api/v1/videos/{id}/hls?height=&from=`
starts one without waiting. A failed job removes its partial output and is
retried by the next request — it never gets stuck "in progress". A job that a
restart interrupted keeps what it had: the next request rescans the entry, and
fills only the gaps. Concurrent viewers of the same video *at the same height*
share one job — a second viewer's `from` re-aims it rather than starting a
second transcode; different heights are different jobs.

Switching quality mid-playback is a client-side matter: load the other
variant's playlist and seek to the current time, passing that time as `from` so
the other height's transcode starts there too. Every rendition cuts on the same
4-second grid (a keyframe is forced at every boundary), so the seek lands
exactly. Each height's `master.m3u8` is a single-variant master (one
`EXT-X-STREAM-INF`, named so hls.js will play it) — there is no one master
listing several renditions for the player to switch between, so nothing here
does adaptive bitrate switching, because that would mean transcoding every
height of every video up front.

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
| `MEDIA_TOKEN_SECONDS` | no | how long a signed media token (cookie and `media_token` URL parameter) stays valid; default 2592000 (30 days) |
| `MEDIA_CACHE_DIR` | no | where derived media is cached; default a temp dir. Must be writable; an HLS rendition of a 1080p hour is ~1.5 GB |
| `MEDIA_CACHE_MAX_BYTES` | no | cache size cap before LRU eviction; default 5 GiB |
| `MEDIA_TRANSCODE_JOBS` | no | concurrent HLS transcodes; default 1, extra requests queue |
| `MEDIA_SEGMENT_WAIT` | no | seconds a request for an HLS segment the transcode has not produced yet blocks before the client is told to come back; default 60 |
| `MEDIA_SEEK_AHEAD_SEGMENTS` | no | how far ahead of the encoder (in 4-second segments) a segment request has to be before the running transcode is re-aimed at it; default 30 (about two minutes) |
| `FFMPEG_PATH` | no | ffmpeg binary; default `ffmpeg` on `PATH` |
| `MEDIA_HWACCEL` | no | `auto` (default), `vaapi` or `off` — hardware transcoding for the HLS rendition; falls back to the CPU per video |
| `MEDIA_VAAPI_DEVICE` | no | DRM render node for VAAPI; default `/dev/dri/renderD128` |
| `SPONSORBLOCK_URL` | no | SponsorBlock server segments are fetched from; default `https://sponsor.ajay.app`. **Empty disables the lookup**, leaving TubeArchivist's download-time snapshot as the only source — what an offline deploy wants |
| `SPONSORBLOCK_CATEGORIES` | no | comma list restricting what is asked for; default asks for everything the service offers and lets each client decide |
| `RYD_URL` | no | Return YouTube Dislike API for dislike counts. **Empty (the default) disables it**, and every video then carries no `stats.dislikes` at all. Unlike SponsorBlock and DeArrow it is asked about a video *by id* — see [Views and votes](#views-and-votes) |
| `APP_NAME` | no | default `Flimm` |
| `PORT` | no | default 8080 |
| `SENTRY_DSN` | no | |
| `ANALYTICS_DISABLED` | no | `true` publishes `analytics_disabled` on `/config`, turning client analytics off for this deployment |
| `LOG_LEVEL` | no | |

The web client's analytics endpoint is a **build arg**, not an env var:
`VITE_UMAMI_URL` / `VITE_UMAMI_WEBSITE_ID`, baked into the bundle at image
build time (`UMAMI_URL` / `UMAMI_WEBSITE_ID` in the xcconfig for the Apple
apps).
