# Flimm for iPhone, iPad and Apple TV

Native clients for a Flimm deployment. They talk only to the Flimm backend
(`/api/v1` and `/media`) — never to TubeArchivist — for the reasons in
[`../docs/apple-apps.md`](../docs/apple-apps.md), which is the implementation
plan this directory is working through.

```
apple/
  project.yml                 xcodegen input — the .xcodeproj is generated, never committed
  Config/                     Secrets.example.xcconfig (committed) / Secrets.xcconfig (yours)
  Shared/                     app code compiled into BOTH targets — AppModel, paging,
                              formatting, palette, authenticated images
  Flimm/                      the iOS + iPadOS app target
    Sources/App/              the two shells, navigation model, routes, PlayerCoordinator
    Sources/Onboarding/       server setup and sign-in
    Sources/Feeds|Channels|Playlists|History|Search|Settings/
    Sources/Player/           the AVPlayerLayer shell, controls, scrubber, Now Playing
    Sources/Components/       video cards, list states, badges
    Sources/Support/          grid columns
  FlimmTV/                    the tvOS app target
    Sources/App/              the tab shell, routes, TVPlayerCoordinator, metrics
    Sources/Onboarding/       server setup, the device-code screen and its QR code
    Sources/Feeds|Channels|Playlists|History|Search|Settings/
    Sources/Player/           the AVPlayerViewController bridge, overlay, Info panel
    Sources/Components/       focus cards, grids, list states
  FlimmKit/                   the shared Swift package: models, API client, auth, playback
```

Two targets, one package. `FlimmKit` holds everything that is not UI;
`Shared/` holds the small amount of app code that is *the same view code* on a
phone and a television. Everything else in `FlimmTV` is written for the focus
engine and a 10-foot reading distance, which is not the phone's layout scaled
up.

`FlimmKit` has no UIKit or SwiftUI dependency and its tests run on the host
toolchain. **Anything worth testing belongs there** — the WebVTT parser, the
chapter and SponsorBlock maths, the codec gate, the subtitle track choice and
the RFC 8628 polling rules all live there for exactly that reason.

One target serves iPhone and iPad, as two renderings of one state. A compact
width gets the `TabView` of Feeds · Channels · Playlists · History; a regular
width gets a `NavigationSplitView` whose sidebar carries the feeds with their
unseen counts, the library, pinned playlists and Settings. Both read
`NavigationModel` — selected section, a `NavigationPath` per section, the
chosen feed — because iPad multitasking flips the size class mid-flow and
swaps the shell underneath the user; anything a shell owned itself would be
lost on every resize. For the same reason the watching session lives in
`PlayerCoordinator` rather than in the player view, and lists are cached by
query in `PagerStore`.

Screens take the API client and the session from the SwiftUI environment and
own no navigation of their own, which is what let the same views serve both
shells (and, later, tvOS).

With a hardware keyboard the player follows the web client: space/`k`
play-pause, `j`/`l` and the arrows ±10 s, `n`/`p` previous and next, `[`/`]`
chapters, `f` full screen, `m` mute, `c` subtitles, `,`/`.` speed. Anywhere in
the app, `/` and ⌘F focus search and ⌘, opens Settings.

## Requirements

- Xcode 26 (iOS 26 SDK); the app deploys back to **iOS 17**.
- `brew install xcodegen swiftlint`

## Getting set up

```bash
cd apple
cp Config/Secrets.example.xcconfig Config/Secrets.xcconfig   # fill in if you have values
xcodegen generate
open Flimm.xcodeproj
```

`Secrets.xcconfig` is gitignored. Both settings are optional for a simulator
build: `DEVELOPMENT_TEAM` is only needed to sign, and an empty `SENTRY_DSN`
disables crash reporting (debug builds never start Sentry regardless).

Re-run `xcodegen generate` after editing `project.yml` or adding a file —
sources are globbed, so new files appear without touching the project.

## Running the tests

```bash
cd apple/FlimmKit && swift test          # the whole package suite
cd apple && swiftlint --strict           # lint, as CI runs it
```

An unsigned simulator build of the app:

```bash
cd apple
xcodebuild -project Flimm.xcodeproj -scheme Flimm \
  -destination 'generic/platform=iOS Simulator' CODE_SIGNING_ALLOWED=NO build
```

The Apple TV app:

```bash
cd apple
xcodebuild -project Flimm.xcodeproj -scheme FlimmTV \
  -destination 'generic/platform=tvOS Simulator' CODE_SIGNING_ALLOWED=NO build
```

Build for an iPad simulator too when touching layout — the shells differ:

```bash
xcrun simctl list devices available | grep iPad
xcodebuild -project Flimm.xcodeproj -scheme Flimm \
  -destination 'platform=iOS Simulator,name=iPad Pro 11-inch (M5)' \
  CODE_SIGNING_ALLOWED=NO build
```

Running it on a booted simulator without Xcode:

```bash
UDID=$(xcrun simctl list devices available -j \
  | python3 -c "import sys,json;d=json.load(sys.stdin)['devices'];print(next(x['udid'] for r in d.values() for x in r if x['name']=='iPhone 17'))")   # or an iPad name
xcrun simctl boot "$UDID"; xcrun simctl bootstatus "$UDID" -b
xcrun simctl install "$UDID" "$(xcodebuild -project Flimm.xcodeproj -scheme Flimm \
  -destination "id=$UDID" -showBuildSettings 2>/dev/null \
  | awk -F' = ' '/ BUILT_PRODUCTS_DIR/{print $2; exit}')/Flimm.app"
xcrun simctl launch "$UDID" dev.winktech.flimm
```

The same for the Apple TV app (create a device from the tvOS runtime first if
`simctl` lists none):

```bash
UDID=$(xcrun simctl list devices available -j \
  | python3 -c "import sys,json;d=json.load(sys.stdin)['devices'];print(next(x['udid'] for r in d.values() for x in r if 'Apple TV' in x['name']))")
xcrun simctl boot "$UDID"; xcrun simctl bootstatus "$UDID" -b
xcrun simctl install "$UDID" "$(xcodebuild -project Flimm.xcodeproj -scheme FlimmTV \
  -destination "id=$UDID" -showBuildSettings 2>/dev/null \
  | awk -F' = ' '/ BUILT_PRODUCTS_DIR/{print $2; exit}')/FlimmTV.app"
xcrun simctl launch "$UDID" dev.winktech.flimm.tv
xcrun simctl io "$UDID" screenshot /tmp/flimm-tv.png
```

Without a server to point it at, the setup screen is as far as it goes — which
is still the fastest way to check the four probe failure states.

> The Keychain does not work in an **unsigned** simulator build: every read
> comes back empty, which looks exactly like being signed out immediately
> after signing in. Sign the simulator build before debugging auth.

## Signing in

The server URL is the only thing a user enters. `GET /api/v1/config` supplies
the OIDC issuer and client id, and the app takes it from there.

**iPhone and iPad** run Authorization Code + PKCE in an
`ASWebAuthenticationSession`. The provider must allow this exact native
redirect URI on the client:

```
dev.winktech.flimm://auth
```

**Apple TV** has no browser and no `ASWebAuthenticationSession`, so it runs the
OIDC **device authorization grant** (RFC 8628): the provider must advertise
`device_authorization_endpoint` and accept the grant for the same client id.
There is no redirect URI on this path and no fallback on this platform — if the
grant is not enabled, the TV can only say so.

Either way the provider must grant `offline_access`, or there is no refresh
token and the app logs out as soon as the access token expires.

## Configuration reference

| Setting | Where | Notes |
|---|---|---|
| `DEVELOPMENT_TEAM` | `Config/Secrets.xcconfig` | Apple Developer team id; only needed to sign |
| `SENTRY_DSN` | `Config/Secrets.xcconfig` | optional; empty disables Sentry. `//` starts a comment in xcconfig, so split the slashes: `https:/$()/…` |
| `MARKETING_VERSION` / `CURRENT_PROJECT_VERSION` | `project.yml` | the plist references them, never literals |

## CI

`.github/workflows/apple.yaml` runs on `macos-26` whenever `apple/**` changes:
`xcodegen generate`, `swiftlint --strict`, the FlimmKit tests, and unsigned iOS
and tvOS simulator builds. TestFlight workflows come with a later phase.
