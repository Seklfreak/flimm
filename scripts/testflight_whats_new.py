#!/usr/bin/env python3
"""Push a build's changelog to TestFlight as its "What to Test" notes.

After testflight.yaml uploads a build, this finds that build in App Store Connect
(by build number) and sets its beta build localization `whatsNew` to the release
changelog, so testers see what changed. Idempotent: it creates the localization
if missing and updates it otherwise.

Auth is the App Store Connect API key (an ES256-signed JWT). It never fails the
release — if the build hasn't finished registering, or anything else goes wrong,
it emits a warning and exits 0 (the upload itself already succeeded).

Usage:
  testflight_whats_new.py --bundle-id … --version … --build … --notes-file … \
      --key-id … --issuer-id … --key-path …
"""
import argparse
import json
import sys
import time
import urllib.error
import urllib.request

import jwt  # PyJWT (pulls in `cryptography` for ES256)

API = "https://api.appstoreconnect.apple.com"
WHATS_NEW_MAX = 4000  # App Store Connect's limit for the "What to Test" field.


def make_token(key_id: str, issuer_id: str, key_path: str) -> str:
    with open(key_path) as f:
        private_key = f.read()
    now = int(time.time())
    payload = {"iss": issuer_id, "iat": now, "exp": now + 600, "aud": "appstoreconnect-v1"}
    return jwt.encode(payload, private_key, algorithm="ES256", headers={"kid": key_id, "typ": "JWT"})


def api(method: str, url: str, token: str, body: dict | None = None) -> dict:
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method)
    r.add_header("Authorization", f"Bearer {token}")
    r.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(r) as resp:
        raw = resp.read()
        return json.loads(raw) if raw else {}


def warn_exit(msg: str) -> None:
    # Print a GitHub Actions warning annotation but succeed — the changelog is a
    # nice-to-have on top of an already-successful upload.
    sys.stderr.write(f"::warning::What-to-Test: {msg}\n")
    sys.exit(0)


def clean_notes(text: str) -> str:
    """TestFlight's field is plain text — drop markdown heading hashes and clamp."""
    lines = []
    for line in text.splitlines():
        s = line.rstrip()
        if s.lstrip().startswith("#"):
            s = s.lstrip("#").strip()
        lines.append(s)
    out = "\n".join(lines).strip()
    if len(out) > WHATS_NEW_MAX:
        out = out[: WHATS_NEW_MAX - 1].rstrip() + "…"
    return out or "Bug fixes and improvements."


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--bundle-id", required=True)
    ap.add_argument("--version", required=True, help="marketing version, for logging")
    ap.add_argument("--build", required=True, help="build number (CFBundleVersion)")
    ap.add_argument("--notes-file", required=True)
    ap.add_argument("--key-id", required=True)
    ap.add_argument("--issuer-id", required=True)
    ap.add_argument("--key-path", required=True)
    ap.add_argument("--locale", default="en-US")
    ap.add_argument("--timeout", type=int, default=1200, help="seconds to wait for processing")
    args = ap.parse_args()

    with open(args.notes_file) as f:
        notes = clean_notes(f.read())
    label = f"{args.version} ({args.build})"

    def token() -> str:
        try:
            return make_token(args.key_id, args.issuer_id, args.key_path)
        except Exception as e:  # noqa: BLE001 — a bad key shouldn't fail the release
            warn_exit(f"could not build an App Store Connect token: {e}")

    tok = token()

    # Resolve the app id from the bundle id.
    try:
        apps = api("GET", f"{API}/v1/apps?filter[bundleId]={args.bundle_id}&limit=1", tok)
    except urllib.error.HTTPError as e:
        warn_exit(f"apps lookup failed ({e.code}): {e.read().decode(errors='replace')}")
    app = (apps.get("data") or [])
    if not app:
        warn_exit(f"no app found for bundle id {args.bundle_id}")
    app_id = app[0]["id"]

    # Poll for the build. The build number (GITHUB_RUN_NUMBER) is globally unique,
    # so app + version filters it exactly. Processing can lag the upload by minutes,
    # and the resource only appears once App Store Connect has registered it.
    build_url = f"{API}/v1/builds?filter[app]={app_id}&filter[version]={args.build}&limit=1"
    build_id = None
    deadline = time.time() + args.timeout
    while time.time() < deadline:
        try:
            builds = api("GET", build_url, tok)
            found = builds.get("data") or []
            if found:
                build_id = found[0]["id"]
                break
            print(f"Build {label} not registered yet; waiting…")
        except urllib.error.HTTPError as e:
            if e.code in (401, 403):
                warn_exit(f"not authorized to read builds ({e.code}); check the API key role.")
            print(f"Transient error polling builds ({e.code}); retrying…")
        time.sleep(30)
        tok = token()  # refresh — a 10-min JWT can expire across a long wait
    if not build_id:
        warn_exit(f"build {label} did not appear within {args.timeout}s; skipping "
                  "(the upload still succeeded).")

    # Set whatsNew: PATCH the existing localization for this locale, else POST one.
    try:
        locs = api("GET", f"{API}/v1/builds/{build_id}/betaBuildLocalizations?limit=50", tok)
        existing = next((loc for loc in (locs.get("data") or [])
                         if (loc.get("attributes") or {}).get("locale") == args.locale), None)
        if existing:
            api("PATCH", f"{API}/v1/betaBuildLocalizations/{existing['id']}", tok,
                {"data": {"type": "betaBuildLocalizations", "id": existing["id"],
                          "attributes": {"whatsNew": notes}}})
        else:
            api("POST", f"{API}/v1/betaBuildLocalizations", tok,
                {"data": {"type": "betaBuildLocalizations",
                          "attributes": {"locale": args.locale, "whatsNew": notes},
                          "relationships": {"build": {"data": {"type": "builds", "id": build_id}}}}})
    except urllib.error.HTTPError as e:
        warn_exit(f"setting What-to-Test failed ({e.code}): {e.read().decode(errors='replace')}")

    print(f"Set What-to-Test for build {label} ({len(notes)} chars).")


if __name__ == "__main__":
    main()
