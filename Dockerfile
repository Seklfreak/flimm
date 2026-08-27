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
# ffmpeg derives every alternative rendition: the audio-only ones (a remux) and
# the compatible H.264/AAC HLS one (a real transcode). Alpine's ffmpeg carries
# what that needs — libx264 to encode, libdav1d and libvpx to decode the AV1
# and VP9 the archive holds, and the hls muxer.
#
# intel-media-driver is the VAAPI driver (iHD) for Intel graphics from Broadwell
# on: with a /dev/dri render node passed into the container, the HLS transcode
# decodes and encodes on the iGPU instead of the CPU. It costs ~43 MB in the
# image (143 MB → 186 MB) and nothing at runtime on a host without a GPU —
# MEDIA_HWACCEL=auto finds no render node and stays on the CPU. libva is the
# API it plugs into; the legacy i965 driver is deliberately not installed, as
# nothing iHD does not cover is in scope. See docs/deploy.md.
RUN apk add --no-cache ca-certificates tzdata ffmpeg intel-media-driver libva && adduser -D -u 1000 app
# Name the driver rather than letting libva probe for it: probing picks by PCI
# id and quietly lands on i965 (absent here) for some devices, which surfaces
# as a per-video hardware failure and a silent fallback to the CPU instead of
# an obvious one.
ENV LIBVA_DRIVER_NAME=iHD
USER app
COPY --from=backend /archive /archive
EXPOSE 8080
ENTRYPOINT ["/archive"]
