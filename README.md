# Archive

A self-hosted client for a single [TubeArchivist](https://github.com/tubearchivist/tubearchivist)
instance. TubeArchivist downloads and indexes your YouTube channels; Archive is
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
  pinned feed the app opens on, and a built-in **Everything** feed.
- **Channels** directory — search, sort, see which feeds each channel is in,
  find channels in no feed; add/remove from feeds right on the channel page.
- **Resume & seen state per user** — heartbeat progress, automatic resume
  with *Start over*, auto-seen at ~90 %, *Mark seen* / *Mark unseen*, mark a
  whole feed or channel seen.
- **History** — grouped by day, in-progress rows resume in place, entries can
  be hidden.
- **Playlists** — your custom playlists and channel playlists with watched
  progress and a resume target; create, reorder, delete through TubeArchivist.
- **Search** across titles, channels, playlists **and subtitle text**; a
  subtitle hit jumps straight to that timestamp.
- **Player** — archived and auto-generated subtitle tracks, SponsorBlock
  segment skipping, playback speed, autoplay with context-aware *Up next*.
- **Preferences** per user (autoplay, speed, subtitles, theme…).
- **OIDC login** with any provider; media streams through the backend so
  TubeArchivist itself never has to be exposed.

See [docs/design.md](docs/design.md) for the product model and
[docs/roadmap.md](docs/roadmap.md) for what's next (native Apple apps).

## Architecture

```mermaid
flowchart LR
  subgraph clients [Clients]
    Web["Web app<br/>(React, embedded)"]
    Native["iOS / iPadOS / tvOS<br/>(roadmap)"]
  end
  subgraph archive [Archive container]
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

Clients talk **only** to the Archive backend. The backend validates OIDC
tokens, keeps the state TubeArchivist lacks in Postgres, reads everything else
live from TubeArchivist with a server-side API token, and reverse-proxies video
(with range requests), subtitles and thumbnails.

## How it maps onto TubeArchivist

| Archive | TubeArchivist |
|---|---|
| Video lists, channels, playlists, search, similar, comments | read live from the TA API (short per-user caches) |
| Watched flag | `POST /api/watched/` — written on every change |
| Resume position | `POST/DELETE /api/video/{id}/progress/` — written on every heartbeat |
| Custom playlists | created / reordered / deleted via `/api/playlist/custom/` |
| Feeds, history, prefs, per-user watch events | **Archive's Postgres only** — TA has no per-user model |
| Video, subtitles, thumbnails | proxied from TA's `/media` and `/cache` with the API token |

The full contract — every endpoint, object and the exact TA calls behind them —
is in [docs/api.md](docs/api.md).

## Requirements

- A **TubeArchivist** instance (v0.4+) and an **API token** for it
  (Settings → User). Ideally reachable over the network from Archive without an
  auth proxy in between.
- **Postgres** 15+.
- An **OIDC provider** — any: Authentik, Keycloak, Auth0, Zitadel, Dex,
  Google… Archive needs a public (PKCE) client with redirect URI
  `https://<host>/auth/callback`.
- HTTPS in front of Archive (the media cookie is `Secure`).

## Configuration

All configuration is via environment variables.

| Var | Required | Notes |
|---|---|---|
| `TA_URL` | yes | e.g. `http://tubearchivist:8000` (in-cluster) or the public URL |
| `TA_TOKEN` | yes | TubeArchivist API token (Settings → User) |
| `DATABASE_URL` | yes | Postgres DSN |
| `MEDIA_TOKEN_SECRET` | yes | HMAC secret for the media cookie (`openssl rand -hex 32`) |
| `PUBLIC_URL` | yes | the `https://` origin users reach Archive on; used for cookies/CORS |
| `OIDC_ISSUER` | unless `AUTH_DISABLED=true` | issuer URL (discovery at `<issuer>/.well-known/openid-configuration`) |
| `OIDC_CLIENT_ID` | unless `AUTH_DISABLED=true` | public client id |
| `AUTH_DISABLED` | no | `true` skips auth and uses a fixed dev user — **dev only** |
| `ADMIN_EMAILS` | no | comma-separated; admins see `/healthz` details |
| `APP_NAME` | no | default `Archive` |
| `PORT` | no | default `8080` |
| `SENTRY_DSN` | no | backend error reporting |
| `VITE_SENTRY_DSN` | no | frontend error reporting; a **build arg**, baked into the bundle at image build time (not a runtime env var) |
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

`make build` produces the single binary with the frontend embedded;
`make sqlc` regenerates the query code after editing `internal/db/queries`.
With `AUTH_DISABLED=true` the media cookie is still issued but not marked
`Secure`, so plain-HTTP localhost works.

Tests: `go test ./...` and `cd frontend && npm run lint && npm run build`.

## Container image

```
ghcr.io/seklfreak/archive-client:<version>   # semver, e.g. 1.2.3
ghcr.io/seklfreak/archive-client:latest      # newest release
```

`linux/amd64`. Listens on `PORT` (8080), serves the API, media proxy and web
app from one port, runs migrations on start.

## Deploying

Archive is a stateless single container next to Postgres and TubeArchivist.
[docs/deploy.md](docs/deploy.md) has a generic Kubernetes example (Deployment,
Service, Ingress, Secret, ConfigMap) plus notes on placing it in the same
cluster as TubeArchivist so streaming doesn't cross an auth proxy, OIDC
redirect URIs, and the HTTPS requirement. Docker Compose works the same way:
one service, the env vars above, HTTPS from your reverse proxy.

## Roadmap

Native iOS / iPadOS / tvOS apps on the same API are next; see
[docs/roadmap.md](docs/roadmap.md).

## License

[MIT](LICENSE) © 2026 Sebastian Winkler. Not affiliated with TubeArchivist.
