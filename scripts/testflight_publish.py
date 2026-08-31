#!/usr/bin/env python3
"""Give an uploaded TestFlight build its release notes and hand it to testers.

After testflight.yaml uploads a build, this finds that build in App Store Connect
(by build number), sets its beta build localization `whatsNew` to the release
changelog, adds it to the named beta groups, expires any older build still
sitting in beta review, and submits the new one for review. Notes first,
distribution second: adding the build is what notifies testers, and they should
have the changelog by then. Every step is idempotent.

Distribution has to happen here rather than being a group setting. A group's
`hasAccessToAllBuilds` ("automatic distribution") is create-only — App Store
Connect refuses it in a PATCH — so a group made without it can never be
switched over, and every build has to be attached explicitly.

Auth is the App Store Connect API key (an ES256-signed JWT). It never fails the
release — if the build hasn't finished registering, or anything else goes wrong,
it emits a warning and exits 0 (the upload itself already succeeded).

Usage:
  testflight_publish.py --bundle-id … --version … --build … --notes-file … \
      --key-id … --issuer-id … --key-path … [--groups Public,Internal]
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
    ap.add_argument("--groups", default="",
                    help="comma-separated beta group names to distribute the build to")
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
                state = (found[0].get("attributes") or {}).get("processingState")
                # A build still processing can't be handed to a group, so wait
                # for it when there's distribution to do.
                if state == "VALID" or not args.groups:
                    break
                print(f"Build {label} is {state}; waiting for processing…")
                build_id = None
            else:
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
    distribute(app_id, build_id, args.build, label, args.groups, tok)


def distribute(app_id: str, build_id: str, build_number: str, label: str,
               group_names: str, tok: str) -> None:
    """Add the build to each named beta group — this is what releases it to testers.

    External groups only: an internal group has every build by definition, and
    App Store Connect answers 422 for one.
    """
    wanted = [n.strip().casefold() for n in group_names.split(",") if n.strip()]
    if not wanted:
        return
    try:
        groups = api("GET", f"{API}/v1/apps/{app_id}/betaGroups?limit=200", tok)
    except urllib.error.HTTPError as e:
        warn_exit(f"beta group lookup failed ({e.code}): {e.read().decode(errors='replace')}")

    by_name = {(g["attributes"].get("name") or "").casefold(): g for g in (groups.get("data") or [])}
    attached = False
    for name in wanted:
        group = by_name.get(name)
        if not group:
            sys.stderr.write(f"::warning::TestFlight: no beta group named {name!r}; "
                             f"build {label} not distributed to it.\n")
            continue
        try:
            api("POST", f"{API}/v1/betaGroups/{group['id']}/relationships/builds", tok,
                {"data": [{"type": "builds", "id": build_id}]})
            attached = True
            print(f"Distributed build {label} to {group['attributes']['name']}.")
        except urllib.error.HTTPError as e:
            # Already in the group is a success, not a failure: a re-run of the
            # job shouldn't look broken.
            body = e.read().decode(errors="replace")
            if e.code == 409 and "already" in body.lower():
                attached = True
                print(f"Build {label} was already in {group['attributes']['name']}.")
                continue
            sys.stderr.write(f"::warning::TestFlight: distributing build {label} to "
                             f"{group['attributes']['name']} failed ({e.code}): {body}\n")
    if attached:
        expire_superseded(app_id, build_id, build_number, label, tok)
        submit_for_review(build_id, label, tok)


def expire_superseded(app_id: str, build_id: str, build_number: str, label: str,
                      tok: str) -> None:
    """Expire older builds still waiting on (or in) beta review.

    Every release bumps the marketing version, so each public build needs a
    real Beta App Review, and a stale build queued ahead of this one only adds
    hours before testers see the new one. There is no API to withdraw a
    betaAppReviewSubmission — expiring the build is what cancels its review.
    """
    url = (f"{API}/v1/builds?filter[app]={app_id}&filter[expired]=false"
           "&filter[betaAppReviewSubmission.betaReviewState]=WAITING_FOR_REVIEW,IN_REVIEW"
           "&limit=200")
    try:
        pending = api("GET", url, tok)
    except urllib.error.HTTPError as e:
        sys.stderr.write(f"::warning::TestFlight: listing builds pending review failed "
                         f"({e.code}): {e.read().decode(errors='replace')}\n")
        return
    for build in pending.get("data") or []:
        version = (build.get("attributes") or {}).get("version") or ""
        # Build numbers are GITHUB_RUN_NUMBER, so numeric order is release
        # order: only expire strictly older builds, so a re-run of an old tag
        # can never take down a newer build's review.
        try:
            superseded = build["id"] != build_id and int(version) < int(build_number)
        except ValueError:
            superseded = False
        if not superseded:
            continue
        try:
            api("PATCH", f"{API}/v1/builds/{build['id']}", tok,
                {"data": {"type": "builds", "id": build["id"],
                          "attributes": {"expired": True}}})
            print(f"Expired build {version}, superseded by {label} — its pending "
                  "beta review is withdrawn.")
        except urllib.error.HTTPError as e:
            sys.stderr.write(f"::warning::TestFlight: expiring superseded build {version} "
                             f"failed ({e.code}): {e.read().decode(errors='replace')}\n")


def submit_for_review(build_id: str, label: str, tok: str) -> None:
    """Ask for beta review, which is what actually opens the build to testers.

    A build sits at "Ready to Submit" until this exists — being in an external
    group is not a submission. Builds after the first of an approved version
    are usually waved through, but they still have to be asked for.
    """
    try:
        current = api("GET", f"{API}/v1/builds/{build_id}/betaAppReviewSubmission", tok)
        if current.get("data"):
            state = current["data"]["attributes"].get("betaReviewState")
            print(f"Build {label} is already submitted for beta review ({state}).")
            return
        api("POST", f"{API}/v1/betaAppReviewSubmissions", tok,
            {"data": {"type": "betaAppReviewSubmissions",
                      "relationships": {"build": {"data": {"type": "builds", "id": build_id}}}}})
        print(f"Submitted build {label} for beta review.")
    except urllib.error.HTTPError as e:
        sys.stderr.write(f"::warning::TestFlight: submitting build {label} for beta review "
                         f"failed ({e.code}): {e.read().decode(errors='replace')}\n")


if __name__ == "__main__":
    main()
