# CLAUDE.md

Guidance for working in this repo. Read before making changes.

## This repo is public and generic

Flimm is published as a generic self-hosted TubeArchivist client. Nothing in
this repo may reference a specific deployment: **no homelab hostnames,
Kubernetes namespaces, kubeconfig paths, cluster-internal service names,
Sentry DSNs, tokens, or personal data**. Examples use `flimm.example.com`,
`tubearchivist.example.com`, `tubearchivist:8000` and the like. Deployment
specifics (manifests, secrets, image pins) live outside this repo; the only
deployment doc here is the generic `docs/deploy.md`.

## Keep the docs in sync

When a change affects behavior, features, endpoints, the data model, config, or
setup, update the docs in the **same change**:

- **`README.md`** — user/dev-facing: intro, features, requirements, the
  configuration table, running locally, image name.
- **`docs/api.md`** — the API contract (endpoints, objects, TA mapping, env
  vars). Clients (web and native) are written against it, so it is normative.
- **`docs/design.md`** — the product model; update when a concept changes.

There is deliberately **no roadmap file**. It became a changelog, then a list of
things that were either already built or not worth building, and both halves
went stale without anyone noticing. Git history and the releases record what
shipped; what is coming is decided in conversation, not tracked in the repo.
Don't reintroduce `docs/roadmap.md`, a "Done" section, or a TODO list in the
docs.

If a change makes a doc wrong, fixing the doc is part of the change, not a
follow-up.

## Layout

- Repo root is the Go module (chi, pgx + sqlc, golang-migrate, go-oidc).
  - `cmd/server` — the HTTP server: `/api/v1/*` JSON API, `/media/*` reverse
    proxy to TubeArchivist, and the embedded frontend for everything else.
  - `internal/ta` — TubeArchivist client behind an interface so handlers can be
    tested against a fake.
  - `internal/db/migrations` — SQL migrations; `internal/db/queries` — sqlc
    input; `internal/db/sqlc` — generated (do **not** hand-edit).
- `frontend/` — React + TypeScript + Vite. `frontend/dist` is `//go:embed`-ed
  into the binary, so the whole app ships as one container image.
- `apple/` — the native clients: `FlimmKit` (Swift package: models, API
  client, auth, playback logic — no UI), `Flimm` (iPhone + iPad, one target),
  `FlimmTV` (Apple TV), `Shared/` (views both targets compile). xcodegen
  `project.yml`; the `.xcodeproj` is generated and gitignored.
- `docs/` — `api.md` (contract), `design.md`, `deploy.md`, `apple-apps.md`.

## One product, every platform

Flimm is one product with four clients: web, iPhone, iPad and Apple TV. A
change is not done when it works on the platform you happened to start on.

- **Every behaviour change ships to all clients where it makes sense** — a new
  player feature, a preference, a list, a fix in how progress or resume is
  reported — in the **same change**. Do it in the web frontend and in both
  Apple targets (`Flimm` and `FlimmTV`); the iPad is the same target as the
  iPhone, so check its regular-width layout too.
- **Logic lives in one place.** Anything a client decides — codec gating,
  quality choice, chapter math, WebVTT, progress heartbeats — goes in
  `FlimmKit` (Apple) or a shared module (web), never duplicated per target;
  the tvOS and iOS players must call the same code. Anything the *server* can
  decide (feed composition, watched state, shuffle order, resume position,
  renditions) stays on the server so clients cannot drift.
- **API changes name their clients.** When `api.md` gains a field, endpoint
  or parameter, update every client that should use it in the same change,
  and say in the commit which platforms picked it up.
- "Does not make sense" is a deliberate, stated decision (e.g. tvOS has no
  feed editor, the web plays the archive directly and needs no HLS rendition),
  recorded in `docs/apple-apps.md` or `docs/design.md` — not a platform
  quietly left behind.

## Toolchain

- **Go 1.26+.**
- **Node 24** for the frontend (matches CI). Use nvm/mise if the machine's
  default `node` is older — old versions fail in confusing ways.
- Postgres for local dev; see README "Running locally".

## Database / sqlc

- After editing `internal/db/queries/*.sql` or adding a migration, regenerate
  with `make sqlc` (pinned sqlc version in the Makefile). Never edit
  `internal/db/sqlc/*` by hand.
- Migrations are embedded and run on server boot. Add paired
  `NNN_name.up.sql` / `NNN_name.down.sql`.
- Flimm stores only what TubeArchivist cannot: users, feeds, watch events,
  history, prefs. Video/channel/playlist data is read from TA and never copied
  into Postgres beyond ids.

## Auth & access model (important)

- Clients send an OIDC access token (`Authorization: Bearer <jwt>`) on every
  `/api/v1/*` request. The auth middleware validates it against
  `OIDC_ISSUER` / `OIDC_CLIENT_ID`, upserts a `users` row keyed by `sub`, and
  puts the user id + `isAdmin` in the request context. `AUTH_DISABLED=true`
  → fixed dev user, treated as admin. Admin gating uses the `ADMIN_EMAILS`
  allowlist.
- All per-user state (feeds, watch events, history, prefs) is scoped by user
  id in every query. A feed/history id that belongs to another user returns
  **404, not 403**, so existence isn't leaked. When adding an endpoint that
  touches user data, scope it the same way and add an isolation test (user A
  must not reach user B's data).
- `/media/*` accepts a Bearer header **or** the `flimm_media` cookie (signed
  with `MEDIA_TOKEN_SECRET`, 12h, HttpOnly, Secure, SameSite=Lax) because
  `<video>` / `AVPlayer` cannot always set headers. Never relax the cookie
  flags; never serve media unauthenticated.
- The TubeArchivist token (`TA_TOKEN`) is server-side only and must never reach
  a client, a log line, or an error message.

## Frontend conventions

- Talk only to the Flimm backend (`/api/v1`, `/media`) — never to
  TubeArchivist directly.
- Modals/overlays must render through a **portal to `document.body`**
  (`createPortal`). A header with `backdrop-blur` creates a containing block
  for `position: fixed` descendants, so a `fixed inset-0` overlay rendered
  inside it is sized to the header, not the viewport.
- Obey the Rules of Hooks. `npm run lint` (ESLint + `react-hooks`) catches
  hook-order bugs that `tsc` and `vite build` do **not** (a hook after an
  early `return` → blank page). Lint runs in CI.
- Progress heartbeats go through `POST /videos/{id}/progress`; on a media 401
  call `POST /session/media` once and retry.

## See it work before calling it done

**A user-visible change is confirmed against the dev stack, not reasoned
about.** Tests and a clean build say the code compiles and the logic holds;
they say nothing about whether a panel is readable, a caption sits where it
should, or a screen shows two spinners. The stack exists precisely so that
can be checked, and every platform can reach it:

```sh
go run ./cmd/fake-ta   # :8001, with real playable media (see the README)
TA_URL=http://localhost:8001 TA_TOKEN=dev SPONSORBLOCK_URL= \
  AUTH_DISABLED=true PUBLIC_URL=http://localhost:8080 \
  DATABASE_URL=... MEDIA_TOKEN_SECRET=dev go run ./cmd/server
```

- **Web**: `cd frontend && npm run dev`, then drive it in a browser.
- **iPhone / iPad / Apple TV**: run the target in a simulator and connect it to
  `http://localhost:8080` — `AUTH_DISABLED=true` means no sign-in step. Two
  things make this work without a human at the keyboard: the stored server can
  be written straight into the app container
  (`xcrun simctl spawn <dev> defaults write dev.winktech.flimm.tv flimm.server
  -data <hex of {"baseURL":…,"config":…}>`), and the TV app opens a video
  directly in **Debug** builds when `FLIMM_PLAY_VIDEO=<id>` is in its
  environment (`SIMCTL_CHILD_FLIMM_PLAY_VIDEO=… xcrun simctl launch …`), which
  is the only way to reach a screen that exists during playback — on the phone
  and iPad too (`dev.winktech.flimm`). `FLIMM_PLAY_FEED=<id>` /
  `FLIMM_PLAY_PLAYLIST=<id>` open it *in* that context, which is what reaches
  the states that depend on one: the **end of a list**, where up next turns
  into suggestions and autoplay has to stop, is otherwise untestable without
  tapping. The same door
  opens a tab (`FLIMM_OPEN_TAB`), a feed (`FLIMM_OPEN_FEED=<name>`) and puts
  the remote's focus on a feed chip (`FLIMM_FOCUS_FEED=<name>`) — **focus is
  invisible to a screenshot otherwise**, and a state nobody can see is a state
  nobody checks: the feed row shipped for weeks with no focus indication at
  all.
- **A screen that only appears while something is slow** — the
  compatible-rendition wait, most of all — can be held open by taking the
  single transcode slot first: run the server with `MEDIA_TRANSCODE_JOBS=1`,
  `POST /videos/<id>/hls?height=480`, then play the same video, whose 720
  rendition now has to queue.
- The fake's catalogue is built for exactly this: a running-timer picture so a
  seek or a resume can be checked by eye, embedded chapters, SponsorBlock
  segments, WebVTT subtitles that name their own timestamps, and a VP9 video
  Apple hardware cannot decode, which is the only way to reach the codec gate
  and the compatible-rendition wait.

Screenshot what changed. If a change genuinely cannot be reached this way, say
so plainly rather than implying it was seen.

## Before committing

- Backend: `golangci-lint run ./... && go build ./... && go test ./...`
  (config in `.golangci.yml`; it includes govet). **Check the version first:**
  CI pins one in `.github/workflows/test.yaml` and each release adds rules — an
  older local binary reports "0 issues" on code CI then rejects, which is a red
  main you did not see coming. `golangci-lint --version`, and if it is behind,
  run the pinned one the way CI installs it.
- Frontend: `cd frontend && npm run lint && npm run build`.
- Apple: `cd apple && xcodegen generate && swiftlint --strict`, then
  `cd FlimmKit && swift test` (check swift's own exit status, not a grep's),
  and build **both** schemes unsigned: `Flimm` for an iPhone *and* an iPad
  simulator, `FlimmTV` for `generic/platform=tvOS Simulator` — zero warnings.
- Go imports follow goimports grouping with the local module
  (`github.com/Seklfreak/flimm/...`) last.
- Handler tests use the fake TA client and a fake querier; set only what a test
  needs.
- Re-read the "public and generic" rule above before adding any example value.

## CI

`.github/workflows/test.yaml` runs golangci-lint + `go test` and the frontend
`lint` + `build` on every push/PR. Green `main` auto-cuts a versioned release
(`release.yaml`, Seklfreak/ai-release-action) which dispatches `build.yaml` to
push `ghcr.io/seklfreak/flimm:<version>` and `:latest`. Keep both
test jobs green. Docs/CI-only commits do not cut a release.
