FROM node:24-alpine AS frontend
# Baked into the bundle as import.meta.env.VITE_*: the release version.
ARG APP_VERSION=dev
ENV VITE_APP_VERSION=${APP_VERSION}
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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${APP_VERSION}" -o /archive ./cmd/server

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 1000 app
USER app
COPY --from=backend /archive /archive
EXPOSE 8080
ENTRYPOINT ["/archive"]
