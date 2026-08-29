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

    @State private var app: AppModel?
    @State private var player = TVPlayerCoordinator()
    /// Per-device playback settings (video quality) — never a server
    /// preference, and not tied to the account.
    @State private var playback = PlaybackSettings()

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
            return
        }
        if app?.client !== client {
            app = AppModel(client: client)
        }
        player.configure(app: app, playback: playback)
        openDebugVideo()
    }

    /// Opens a video straight from launch, so a screen that only exists during
    /// playback — the compatible-rendition wait, the info panel, the subtitle
    /// placement — can be reached in a simulator without a remote:
    ///
    ///     xcrun simctl launch --console <device> dev.winktech.flimm.tv
    ///     # with SIMCTL_CHILD_FLIMM_PLAY_VIDEO=<video id> in the environment
    ///
    /// Debug builds only; a shipped app has no such door.
    private func openDebugVideo() {
        #if DEBUG
        guard let id = ProcessInfo.processInfo.environment["FLIMM_PLAY_VIDEO"], !id.isEmpty else { return }
        player.play(id)
        #endif
    }
}
