# Deploying on Kubernetes

Flimm is one container image (`ghcr.io/seklfreak/flimm:<version>`)
that needs a Postgres database, a reachable TubeArchivist and an OIDC provider.
The manifests below are a generic starting point; adapt names, storage class,
ingress class and TLS to your cluster. Every hostname here is an example.

## Layout

```
Browser ──HTTPS──▶ Ingress ──▶ Service ──▶ archive (Deployment)
                                                 │         │
                                        Postgres ┘         └──▶ TubeArchivist (in-cluster Service)
```

Run Flimm **in the same cluster as TubeArchivist** and point `TA_URL` at
TA's in-cluster Service (`http://tubearchivist.<ns>.svc.cluster.local:8000`).
Video streaming is a range-request reverse proxy through Flimm; going through
TA's public hostname would push every byte through your ingress and any
auth proxy (Authentik/oauth2-proxy/Cloudflare Access) in front of TA, which
either breaks range requests or doubles the traffic. Flimm authenticates to
TA with the API token, so TA's own auth is satisfied without a proxy.

## Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: archive-secrets
type: Opaque
stringData:
  TA_TOKEN: "<TubeArchivist API token — Settings → User>"
  DATABASE_URL: "postgres://archive:<password>@postgres.example.svc:5432/archive?sslmode=disable"
  MEDIA_TOKEN_SECRET: "<openssl rand -hex 32>"
  # optional
  # SENTRY_DSN: "https://…"
```

Use a sealed/external secret mechanism rather than committing this.

## ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: archive-config
data:
  TA_URL: "http://tubearchivist.media.svc.cluster.local:8000"
  PUBLIC_URL: "https://flimm.example.com"
  OIDC_ISSUER: "https://auth.example.com/application/o/archive/"
  OIDC_CLIENT_ID: "archive"
  ADMIN_EMAILS: "you@example.com"
  APP_NAME: "Flimm"
  PORT: "8080"
  LOG_LEVEL: "info"
  # Derived media (audio renditions and the compatible HLS video rendition).
  MEDIA_CACHE_DIR: "/cache"
  MEDIA_CACHE_MAX_BYTES: "21474836480"   # 20 GiB
  MEDIA_TRANSCODE_JOBS: "1"
  # auto is the default and needs no GPU; see "Hardware acceleration" below.
  MEDIA_HWACCEL: "auto"
```

See the [configuration table](../README.md#configuration) for every variable.

### Outbound network (SponsorBlock, and the one that is off)

Besides TubeArchivist and the OIDC issuer, the server makes one other outbound
call: SponsorBlock segments and crowd-sourced chapter names are fetched from
`https://sponsor.ajay.app` (`SPONSORBLOCK_URL`). It is a lookup by a four-hex
hash prefix of the video id, so the service is never told which video is
playing, and answers are cached for hours.

An install with no egress should set `SPONSORBLOCK_URL: ""`, which turns the
lookup off and leaves the snapshot TubeArchivist indexed at download time as
the source. Leaving it on without egress is not fatal — a failed lookup falls
back to that same snapshot and is not retried for ten minutes — but it costs a
timeout on the first video detail of each such window.

DeArrow (`DEARROW_URL`) is the same service and the same hash-prefix lookup,
and nothing is asked of it until a viewer turns crowd titles or thumbnails on.

**`RYD_URL` is the exception, and it is off by default.** Return YouTube
Dislike is the only source for the dislike count YouTube stopped publishing in
2021, and it has no hash-prefix endpoint: its API is asked about a video **by
id**. So switching it on means that every video detail view tells a third
party, from this server's address, exactly what is being watched here. Answers
are cached for six hours and a failure is not retried for two minutes, which
bounds how often — but not what. It is a trade an operator should make
knowingly rather than inherit from a default:

```yaml
- name: RYD_URL
  value: "https://returnyoutubedislikeapi.com"
```

With it unset, videos simply carry no dislike count and every client hides that
half of the display.

## Deployment + Service

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: archive
spec:
  replicas: 1
  selector:
    matchLabels: { app: archive }
  template:
    metadata:
      labels: { app: archive }
    spec:
      containers:
        - name: archive
          image: ghcr.io/seklfreak/flimm:0.1.0
          ports:
            - name: http
              containerPort: 8080
          envFrom:
            - configMapRef: { name: archive-config }
            - secretRef: { name: archive-secrets }
          # Readiness asks "can this pod take traffic" and so checks the
          # database; liveness asks "should this pod be restarted" and must
          # not, because restarting cannot fix a database that is away and
          # only adds downtime to the outage.
          readinessProbe:
            httpGet: { path: /api/v1/healthz, port: http }
          livenessProbe:
            httpGet: { path: /api/v1/livez, port: http }
            periodSeconds: 30
          volumeMounts:
            - name: media-cache
              mountPath: /cache
          resources:
            # ffmpeg is the reason for the CPU request: see "Transcoding" below.
            requests: { cpu: 500m, memory: 256Mi }
            limits: { cpu: "4", memory: 1Gi }
          securityContext:
            runAsNonRoot: true
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
      volumes:
        # Derived renditions are a cache in the strict sense — every entry can
        # be rebuilt from TubeArchivist — so emptyDir is right: losing it on a
        # restart costs CPU and nothing else. It does have to exist, though:
        # the root filesystem is read-only, so without a writable
        # MEDIA_CACHE_DIR nothing can be derived at all.
        - name: media-cache
          emptyDir:
            sizeLimit: 24Gi
---
apiVersion: v1
kind: Service
metadata:
  name: archive
spec:
  selector: { app: archive }
  ports:
    - name: http
      port: 80
      targetPort: http
```

Migrations run on boot, so a single replica is the safe default; more replicas
work once the schema is in place (migrations are idempotent and locked) — but
note that the derived-media cache is per pod, so each replica transcodes its
own copy.

### Transcoding (CPU and the media cache)

The compatible video renditions (`/media/hls/…`, see
[api.md](api.md#compatible-video-renditions-hls)) are a **real transcode**:
software AV1 or VP9 decode feeding an x264 (or, above 1080p, an x265) encode,
both CPU-bound. On a modern server core that runs at roughly realtime, so a
40-minute video occupies a core for tens of minutes — the viewer starts
watching within seconds, but the job carries on behind them. On a node with an
Intel iGPU most of this goes away; see
[Hardware acceleration](#hardware-acceleration-intel-vaapi) below. The numbers
here are the CPU ones.

A video offers up to five heights (2160, 1440, 1080, 720, 480 — every one its
source can fill) and the client picks. Each is its own job and its own cache
entry, produced only when something asks for it, so nothing is transcoded up
front — but a client that offers a quality switcher can produce several
renditions of one video over an evening.

- **Give the container several cores.** With a 1-core limit a transcode is
  slower than playback and the player stalls waiting for segments. Four cores
  is a comfortable starting point; ffmpeg is started with `-threads 0` and
  uses what it is given.
- **`MEDIA_TRANSCODE_JOBS` (default 1)** caps concurrent transcodes across
  every video *and every height*. Raise it only if there are cores to spare:
  two transcodes sharing the same cores make both viewers wait longer than
  running them one after the other.
- **A rendition is transcoded resume-first**, not front-to-back: the playlist
  covers the whole video from the first request, and the encoder is pointed at
  wherever the viewer actually is. A player asking for a segment that has not
  been made yet waits for it — up to `MEDIA_SEGMENT_WAIT` (60 s) — rather than
  failing, so a box that transcodes slower than realtime shows up as buffering
  rather than as errors. Raise `MEDIA_SEGMENT_WAIT` on a slow box, or give it
  more cores. `MEDIA_SEEK_AHEAD_SEGMENTS` (30, about two minutes) is how far
  ahead a request has to be before the running encode is restarted at it; there
  is rarely a reason to change it.
- **The transcode reads the archive over a loopback HTTP source**
  (`127.0.0.1`, ephemeral port, per-job nonce) rather than a pipe, because
  seeking to the resume point needs a seekable input. It needs no configuration
  and nothing off the box can reach it, but a container that blocks loopback
  traffic to itself would break transcoding.
- **Size the cache for it.** Roughly, per hour of video:

  | height | codec | disk per hour |
  |---|---|---|
  | 2160 | HEVC | ~1.5–2 GB |
  | 1440 | HEVC | ~1–1.5 GB |
  | 1080 | H.264 | ~1.5 GB |
  | 720 | H.264 | ~0.8 GB |
  | 480 | H.264 | ~0.5–0.8 GB |

  So the 5 GiB default `MEDIA_CACHE_MAX_BYTES` holds a couple of 1080p hours,
  or a single 4K one. Give `MEDIA_CACHE_DIR` a volume with room — 50 GiB or
  more if 4K is going to be watched — and set the cap a little under the volume
  size; entries are evicted least-recently-used, and a rendition being watched
  is never evicted out from under its player.
- **Audio-only renditions are cheap** by comparison (a remux, or an audio
  re-encode) and need none of this. A deployment whose clients never hit the
  codec wall can leave the defaults alone.

### Hardware acceleration (Intel VAAPI)

If the node has an Intel iGPU — anything from Broadwell on; a recent desktop or
NUC chip decodes AV1, VP9, HEVC and H.264 and encodes both H.264 and HEVC in
fixed-function silicon — the transcode can run on it instead of the CPU. Expect
the difference to be large: **a 4K AV1 hour finishes in a handful of minutes
rather than the tens of minutes an x264 encode takes**, and the cores stay free
for everything else. On an Alder Lake iGPU a 1080p H.264 rendition runs at
roughly 13–19× realtime. The renditions are the same either way: H.264
High@4.1 up to 1080p, HEVC Main above it.

The tall renditions in particular want a GPU. HEVC encoding on the CPU
(`libx265`) is several times more expensive than x264 at the same speed preset,
so a 4K rendition on a CPU-only host is an hours-long job — offer 4K there only
if viewers are willing to wait, or keep clients on 1080p.

| Var | Default | Notes |
|---|---|---|
| `MEDIA_HWACCEL` | `auto` | `auto` uses the GPU when the render node exists and the process can open it, and the CPU otherwise; `vaapi` always tries it (for a device that appears after start-up); `off` never does |
| `MEDIA_VAAPI_DEVICE` | `/dev/dri/renderD128` | the DRM render node; only worth setting on a host with more than one GPU |

The decision is made once at start-up and logged on one line — grep the boot
log for `media hardware acceleration` to see whether the GPU was found:

```
level=INFO msg="media hardware acceleration" mode=auto vaapi=true device=/dev/dri/renderD128 reason="render node is usable"
```

**Device access.** The image ships the iHD driver (`intel-media-driver`) and
runs as **uid 1000**, which is not in the host's `render` group, so passing the
device in is not enough on its own — the group has to be granted too. Find its
gid on the node with `stat -c '%g' /dev/dri/renderD128` (commonly 44 `video` or
104/993 `render`, depending on the distribution).

With Docker:

```
docker run --device /dev/dri/renderD128 --group-add <render-gid> ghcr.io/…
```

In Kubernetes, with Intel's device plugin advertising the GPU:

```yaml
    spec:
      containers:
        - name: archive
          resources:
            requests:
              cpu: 500m
              memory: 256Mi
              gpu.intel.com/i915: "1"
            limits:
              cpu: "2"          # a GPU transcode needs far fewer cores
              memory: 1Gi
              gpu.intel.com/i915: "1"
      securityContext:
        # uid 1000 is not in the node's render group; without this the render
        # node is present and unopenable, and MEDIA_HWACCEL=auto stays on the
        # CPU (the start-up log line says so).
        supplementalGroups: [<render-gid>]
```

Without the device plugin, a `hostPath` volume at `/dev/dri` plus the same
`supplementalGroups` works and is what a single-node cluster usually does.

**It never becomes a hard dependency.** `auto` on a node without a GPU is
exactly today's behaviour. And per video, a source the fixed-function decoder
cannot take — 10-bit AV1 is the usual one — fails the hardware attempt, and
Flimm clears the partial rendition and re-runs it in software; the viewer sees
a slower transcode, not an error. Those fallbacks are logged at `warn` as
`hls attempt failed, falling back`, so a GPU that has stopped working shows up
as every video logging it rather than as broken playback.

## Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: archive
  annotations:
    # Video is streamed through the backend; don't let the ingress buffer or
    # cap it. Adjust for your controller (these are ingress-nginx names).
    nginx.ingress.kubernetes.io/proxy-body-size: "0"
    nginx.ingress.kubernetes.io/proxy-buffering: "off"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
spec:
  ingressClassName: nginx
  tls:
    - hosts: [flimm.example.com]
      secretName: archive-tls
  rules:
    - host: flimm.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: archive
                port: { name: http }
```

**HTTPS is required.** The `flimm_media` cookie that lets `<video>` stream
without headers is set with `Secure`, so over plain HTTP the browser drops it
and every media request returns 401. Terminate TLS at the ingress (cert-manager
or your own certificate) and set `PUBLIC_URL` to the `https://` origin.

Do **not** put a forward-auth / auth proxy in front of Flimm: it does its own
OIDC validation, and native apps send Bearer tokens that an auth proxy would
reject.

## OIDC provider

Create a public (PKCE) OIDC client — Authentik, Keycloak, Auth0, Zitadel, Dex
and others all work:

- Grant type: Authorization Code with PKCE, no client secret.
- Redirect URI: `https://flimm.example.com/auth/callback`
  (plus the native apps' custom-scheme URI once you use them, see
  [apple-apps.md](apple-apps.md)).
- Scopes: `openid profile email`.
- The token must carry `sub` (user key) and ideally `email` / `name`;
  `ADMIN_EMAILS` matches against `email`.

Put the issuer URL and client id in the ConfigMap. Users are created on first
login; there is no user management in Flimm itself.

### Native apps

The iOS/iPadOS/tvOS clients (see [apple-apps.md](apple-apps.md)) reuse the same
public OIDC client as the web app — no separate client id, no client secret.
Configure the provider to also allow:

- **Redirect URI**: `dev.winktech.flimm://auth` (custom-scheme, public client,
  Authorization Code + PKCE, no client secret) in addition to the web
  callback above.
- **Scope**: `offline_access` alongside `openid profile email`, so the
  provider issues a refresh token — without it the app silently logs the user
  out when the access token expires, since there's no browser session to
  re-authenticate against.
- **Device authorization grant** (RFC 8628), enabled **for the same client
  id**: this is how the tvOS app signs in ("go to this URL and enter this
  code", with a QR code beside it). It is **not optional on Apple TV** —
  tvOS has no browser and no `ASWebAuthenticationSession`, so there is no
  fallback: without the grant the app can only tell the user that the
  provider doesn't support it. The provider must advertise
  `device_authorization_endpoint` in its discovery document and issue
  `offline_access` on this grant too. The iOS, iPadOS and web clients are
  unaffected either way.

## Postgres

Any Postgres 15+ works: a managed instance, a CloudNativePG cluster, or the
plain `postgres` image with a PVC. Flimm needs one database and a role that
can create tables; it stores only small per-user rows (feeds, watch events,
history, prefs), so a few hundred MB is plenty.

## Upgrading

Bump the image tag. Releases are semver (`ghcr.io/seklfreak/flimm:1.2.3`);
`:latest` tracks the newest release. Migrations run forward automatically on
start; roll back by deploying the previous tag (down-migrations exist but are
not applied automatically).
