#!/usr/bin/env bash
# Smoke test for the release image, run by the `image` job in test.yaml:
#
#   scripts/image-smoke.sh flimm:ci
#
# The image is alpine (with ffmpeg) plus a binary built on the runner with the
# frontend embedded. Renovate auto-merges alpine, Go and frontend bumps once
# Test is green, and nothing else ever starts the image. This does, against a
# throwaway Postgres: migrations, /healthz with the database, the real
# frontend bundle, no embedded sourcemaps, and the ffmpeg encoders the
# transcodes need.
set -euo pipefail

image=${1:?usage: $0 <image>}
port=18080
net=flimm-smoke
cleanup() {
  docker rm -f flimm-smoke flimm-smoke-pg >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

fail() {
  echo "FAIL $*"
  docker logs flimm-smoke 2>&1 | tail -20
  exit 1
}

docker network create "$net" >/dev/null
# The PostgreSQL major the app is developed against.
docker pull -q postgres:18-alpine >/dev/null
docker run -d --name flimm-smoke-pg --network "$net" \
  -e POSTGRES_PASSWORD=smoke -e POSTGRES_DB=flimm postgres:18-alpine >/dev/null
# Over TCP: on first start the entrypoint runs a temporary socket-only server
# for initdb, which a socket check reports ready just before it restarts.
for _ in $(seq 60); do
  docker exec flimm-smoke-pg pg_isready -q -h 127.0.0.1 -U postgres -d flimm 2>/dev/null && break
  sleep 0.5
done

# TubeArchivist is deliberately unreachable: startup only warns about it.
docker run -d --name flimm-smoke --network "$net" -p "$port:8080" \
  -e DATABASE_URL="postgres://postgres:smoke@flimm-smoke-pg:5432/flimm?sslmode=disable" \
  -e TA_URL=http://127.0.0.1:9 -e TA_TOKEN=smoke -e MEDIA_TOKEN_SECRET=smoke \
  -e PUBLIC_URL=http://localhost -e AUTH_DISABLED=true -e ANALYTICS_DISABLED=true \
  "$image" >/dev/null

for _ in $(seq 100); do
  curl -fsS "http://localhost:$port/livez" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -fsS "http://localhost:$port/livez" >/dev/null || fail "livez never answered"
echo "ok   livez (migrations ran)"

health=$(curl -sS "http://localhost:$port/healthz")
grep -q '"db":"ok"' <<<"$health" || fail "healthz: $health"
echo "ok   healthz db ok"

index=$(curl -fsS "http://localhost:$port/") || fail "GET / failed"
asset=$(grep -oE '/assets/[^"]+\.js' <<<"$index" | head -1)
[ -n "$asset" ] || fail "index.html references no /assets/*.js"
# An unknown path falls back to index.html with a 200, so check the body.
body=$(curl -fsS "http://localhost:$port$asset") || fail "GET $asset failed"
case $body in "<!doctype"* | "<!DOCTYPE"*) fail "$asset is not in the image (index.html fallback)" ;; esac
echo "ok   frontend: / and $asset"
if curl -fsS "http://localhost:$port$asset.map" 2>/dev/null | grep -q '"mappings"'; then
  fail "$asset.map is served: sourcemaps must not be embedded"
fi
echo "ok   no sourcemaps served"

encoders=$(docker run --rm --entrypoint ffmpeg "$image" -hide_banner -encoders)
for enc in libx264 libx265 aac; do
  grep -q " $enc " <<<"$encoders" || fail "ffmpeg lacks the $enc encoder"
done
echo "ok   ffmpeg encoders libx264 libx265 aac"

[ "$(docker inspect -f '{{.State.Running}}' flimm-smoke)" = true ] || fail "container exited"
echo "image smoke test passed"
