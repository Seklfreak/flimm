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
```

See the [configuration table](../README.md#configuration) for every variable.

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
          readinessProbe:
            httpGet: { path: /api/v1/healthz, port: http }
          livenessProbe:
            httpGet: { path: /api/v1/healthz, port: http }
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

The compatible video rendition (`/media/hls/…`, see
[api.md](api.md#compatible-video-rendition-hls)) is a **real transcode**:
software AV1 or VP9 decode feeding an x264 encode, both CPU-bound and neither
using a GPU. On a modern server core that runs at roughly realtime, so a
40-minute video occupies a core for tens of minutes — the viewer starts
watching within seconds, but the job carries on behind them.

- **Give the container several cores.** With a 1-core limit a transcode is
  slower than playback and the player stalls waiting for segments. Four cores
  is a comfortable starting point; ffmpeg is started with `-threads 0` and
  uses what it is given.
- **`MEDIA_TRANSCODE_JOBS` (default 1)** caps concurrent transcodes. Raise it
  only if there are cores to spare: two transcodes sharing the same cores make
  both viewers wait longer than running them one after the other.
- **Size the cache for it.** An HLS rendition of a 1080p hour is ~2–3 GB, so
  the 5 GiB default `MEDIA_CACHE_MAX_BYTES` holds only a couple. Give
  `MEDIA_CACHE_DIR` a volume with room and set the cap a little under the
  volume size; entries are evicted least-recently-used.
- **Audio-only renditions are cheap** by comparison (a remux, or an audio
  re-encode) and need none of this. A deployment whose clients never hit the
  codec wall can leave the defaults alone.

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
  (plus the native apps' custom-scheme URI once you use them, see the roadmap).
- Scopes: `openid profile email`.
- The token must carry `sub` (user key) and ideally `email` / `name`;
  `ADMIN_EMAILS` matches against `email`.

Put the issuer URL and client id in the ConfigMap. Users are created on first
login; there is no user management in Flimm itself.

### Native apps

The iOS/iPadOS/tvOS clients (see [roadmap.md](roadmap.md)) reuse the same
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
