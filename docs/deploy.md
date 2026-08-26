# Deploying on Kubernetes

Archive is one container image (`ghcr.io/seklfreak/archive-client:<version>`)
that needs a Postgres database, a reachable TubeArchivist and an OIDC provider.
The manifests below are a generic starting point; adapt names, storage class,
ingress class and TLS to your cluster. Every hostname here is an example.

## Layout

```
Browser ──HTTPS──▶ Ingress ──▶ Service ──▶ archive (Deployment)
                                                 │         │
                                        Postgres ┘         └──▶ TubeArchivist (in-cluster Service)
```

Run Archive **in the same cluster as TubeArchivist** and point `TA_URL` at
TA's in-cluster Service (`http://tubearchivist.<ns>.svc.cluster.local:8000`).
Video streaming is a range-request reverse proxy through Archive; going through
TA's public hostname would push every byte through your ingress and any
auth proxy (Authentik/oauth2-proxy/Cloudflare Access) in front of TA, which
either breaks range requests or doubles the traffic. Archive authenticates to
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
  PUBLIC_URL: "https://archive.example.com"
  OIDC_ISSUER: "https://auth.example.com/application/o/archive/"
  OIDC_CLIENT_ID: "archive"
  ADMIN_EMAILS: "you@example.com"
  APP_NAME: "Archive"
  PORT: "8080"
  LOG_LEVEL: "info"
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
          image: ghcr.io/seklfreak/archive-client:0.1.0
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
          resources:
            requests: { cpu: 50m, memory: 64Mi }
            limits: { memory: 256Mi }
          securityContext:
            runAsNonRoot: true
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
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
work once the schema is in place (migrations are idempotent and locked).

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
    - hosts: [archive.example.com]
      secretName: archive-tls
  rules:
    - host: archive.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: archive
                port: { name: http }
```

**HTTPS is required.** The `archive_media` cookie that lets `<video>` stream
without headers is set with `Secure`, so over plain HTTP the browser drops it
and every media request returns 401. Terminate TLS at the ingress (cert-manager
or your own certificate) and set `PUBLIC_URL` to the `https://` origin.

Do **not** put a forward-auth / auth proxy in front of Archive: it does its own
OIDC validation, and native apps send Bearer tokens that an auth proxy would
reject.

## OIDC provider

Create a public (PKCE) OIDC client — Authentik, Keycloak, Auth0, Zitadel, Dex
and others all work:

- Grant type: Authorization Code with PKCE, no client secret.
- Redirect URI: `https://archive.example.com/auth/callback`
  (plus the native apps' custom-scheme URI once you use them, see the roadmap).
- Scopes: `openid profile email`.
- The token must carry `sub` (user key) and ideally `email` / `name`;
  `ADMIN_EMAILS` matches against `email`.

Put the issuer URL and client id in the ConfigMap. Users are created on first
login; there is no user management in Archive itself.

## Postgres

Any Postgres 15+ works: a managed instance, a CloudNativePG cluster, or the
plain `postgres` image with a PVC. Archive needs one database and a role that
can create tables; it stores only small per-user rows (feeds, watch events,
history, prefs), so a few hundred MB is plenty.

## Upgrading

Bump the image tag. Releases are semver (`ghcr.io/seklfreak/archive-client:1.2.3`);
`:latest` tracks the newest release. Migrations run forward automatically on
start; roll back by deploying the previous tag (down-migrations exist but are
not applied automatically).
