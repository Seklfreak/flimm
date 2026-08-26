FROM node:24-alpine AS frontend
# Baked into the bundle as import.meta.env.VITE_*: the release version and the
# Sentry DSN. Both must be declared here — a build-arg that no stage declares
# is silently dropped, which is how the frontend shipped without Sentry.
ARG APP_VERSION=dev
ARG VITE_SENTRY_DSN=
ENV VITE_APP_VERSION=${APP_VERSION}
ENV VITE_SENTRY_DSN=${VITE_SENTRY_DSN}
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci --no-fund --no-audit
COPY frontend/ ./
RUN npm run build

FROM golang:1.27-alpine AS backend
ARG APP_VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY embed.go ./
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=frontend /app/frontend/dist frontend/dist
# Sourcemaps are built "hidden" for upload to Sentry only; they must not be
# embedded in the binary and served to the public.
RUN find frontend/dist -name '*.map' -delete
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${APP_VERSION}" -o /archive ./cmd/server

FROM alpine:3.24
# ffmpeg derives the audio-only rendition (a remux, not a re-encode).
RUN apk add --no-cache ca-certificates tzdata ffmpeg && adduser -D -u 1000 app
USER app
COPY --from=backend /archive /archive
EXPOSE 8080
ENTRYPOINT ["/archive"]
