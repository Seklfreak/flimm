import FlimmKit
import SwiftUI

/// The root: server setup → device sign-in → the tab bar.
///
/// Same rule as the phone: `AuthSession` never drops to `signedOut` for a
/// transient failure. A TV is the device most likely to be on a network the
/// server is briefly unreachable from, and re-signing in there costs a phone
/// and a typed code.
struct TVRootView: View {
    @Environment(AuthSession.self) private var session
    @Environment(\.scenePhase) private var scenePhase

    @State private var app: AppModel?
    @State private var player = TVPlayerCoordinator()
    /// Per-device playback settings (video quality) — never a server
    /// preference, and not tied to the account.
    @State private var playback = PlaybackSettings()
    /// Offers this television to the account's phones, iPads and browsers as
    /// somewhere to "play on" — for as long as the app is in front, whether or
    /// not anything is playing. A backgrounded app cannot answer, so it stops
    /// offering itself the moment it is not.
    @State private var receiver: RemoteReceiverHost?

    var body: some View {
        Group {
            switch session.state {
            case .loading:
                TVLoadingState(label: "Starting…")
            case .needsServer:
                TVServerSetupView()
            case .signedOut:
                TVSignInView()
            case .signedIn:
                if let app {
                    TVShell()
                        .environment(app)
                        .environment(player)
                        .environment(playback)
                } else {
                    TVLoadingState()
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background { TVPageBackground() }
        .task(id: sessionKey) { syncAppModel() }
        // A TV put to sleep and woken the next day is the same process with
        // yesterday's grid on it; see AppModel.sceneReturned.
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                startReceiving()
                Task { await app?.sceneReturned() }
            } else {
                stopReceiving()
                app?.sceneLeft()
            }
        }
        // A top-shelf item opened from the Home screen. The shelf's actions
        // are URLs — that is the only channel tvOS gives an extension — and
        // playing straight away is what selecting a video does everywhere
        // else in this app.
        .onOpenURL { url in
            guard let id = TopShelfLink.videoID(from: url) else { return }
            player.play(id)
        }
        // A phone in the room handing a video to the television.
        //
        // The TV publishes activities of its own (see ``TVWatchModel``), and an
        // app that names an activity type is a device the system will offer as
        // a destination for it — so it has to be able to act on one, or the
        // offer opens Flimm on the Home screen and does nothing. Only the
        // watch case: a page is not something anybody hands to a television.
        .onContinueUserActivity(Continuation.activityType) { activity in
            guard let continuation = Continuation(userInfo: activity.userInfo ?? [:]),
                  let server = session.server?.baseURL,
                  continuation.matches(server: server),
                  case .watch(let videoId, let position, let context) = continuation.destination else { return }
            player.play(videoId, context: context, startAt: position > 0 ? position : nil)
        }
    }

    /// Changes when the session or the server does, which is exactly when the
    /// `APIClient` behind `AppModel` has to be replaced.
    private var sessionKey: String {
        "\(session.state)|\(session.server?.baseURL.absoluteString ?? "")"
    }

    private func syncAppModel() {
        guard session.state == .signedIn, let client = session.client else {
            app = nil
            player.configure(app: nil, playback: playback)
            stopReceiving()
            receiver = nil
            return
        }
        if app?.client !== client {
            app = AppModel(client: client)
            stopReceiving()
            receiver = RemoteReceiverHost(client: client, device: UIDevice.current.name, platform: "tvos")
        }
        player.configure(app: app, playback: playback)
        if scenePhase == .active { startReceiving() }
        openDebugVideo()
    }

    /// A phone asking for a video opens it exactly as selecting it here would,
    /// from where the phone was and in the list it was playing from — and
    /// replaces whatever was on, which is what "play on the TV" means.
    private func startReceiving() {
        let player = self.player
        receiver?.start { request in
            player.play(request.videoId, context: request.context, startAt: request.position > 0 ? request.position : nil)
        }
    }

    private func stopReceiving() {
        receiver?.stop()
    }

    /// Opens a video straight from launch, so a screen that only exists during
    /// playback — the compatible-rendition wait, the info panel, the subtitle
    /// placement — can be reached in a simulator without a remote:
    ///
    ///     xcrun simctl launch --console <device> dev.winktech.flimm.tv
    ///     # with SIMCTL_CHILD_FLIMM_PLAY_VIDEO=<video id> in the environment
    ///
    /// `FLIMM_PLAY_FEED` / `FLIMM_PLAY_PLAYLIST` open it *in* that context —
    /// the only way to reach what depends on one, the end of a list most of
    /// all, where up next turns into suggestions and autoplay has to stop.
    /// The phone has the same pair (see `ContentView`).
    ///
    /// Debug builds only; a shipped app has no such door.
    private func openDebugVideo() {
        #if DEBUG
        let env = ProcessInfo.processInfo.environment
        guard let id = env["FLIMM_PLAY_VIDEO"], !id.isEmpty else { return }
        var context = PlaybackContext.none
        if let feed = env["FLIMM_PLAY_FEED"], !feed.isEmpty {
            context = PlaybackContext(source: .feed(feed))
        } else if let playlist = env["FLIMM_PLAY_PLAYLIST"], !playlist.isEmpty {
            context = PlaybackContext(source: .playlist(playlist))
        }
        player.play(id, context: context)
        #endif
    }
}
