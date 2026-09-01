import FlimmKit
import SwiftUI

/// The root: setup → sign-in → the app.
///
/// `AuthSession` never drops to `signedOut` for a transient failure — only a
/// dead refresh token does that — so a flaky network keeps the user where they
/// are instead of throwing them back to the setup screen.
struct ContentView: View {
    @Environment(AuthSession.self) private var session
    @Environment(\.scenePhase) private var scenePhase

    @State private var app: AppModel?
    /// Watches for a video playing on another screen of this account's — the
    /// Apple TV — so the companion bar can offer to steer it. It holds one
    /// long-poll request open while the app is on screen, which is why it is
    /// stopped the moment it is not.
    @State private var remote: RemoteControl?
    @State private var player = PlayerCoordinator()
    /// Shared by both shells; see ``RootShell``.
    @State private var nav = NavigationModel()
    /// Per-device playback settings (video quality). Unlike ``Prefs`` these
    /// never leave the device, so they are not tied to the account and outlive
    /// a sign-out.
    @State private var playback = PlaybackSettings()

    var body: some View {
        Group {
            switch session.state {
            case .loading:
                LoadingState(label: "Starting…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Palette.background)
            case .needsServer:
                ServerSetupView()
            case .signedOut:
                SignInView()
            case .signedIn:
                if let app, let remote {
                    RootShell()
                        .environment(app)
                        .environment(remote)
                        .environment(player)
                        .environment(nav)
                        .environment(playback)
                } else {
                    LoadingState()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(Palette.background)
                }
            }
        }
        .preferredColorScheme(app?.prefs.theme.colorScheme)
        .task(id: sessionKey) { syncAppModel() }
        // A backgrounded phone has nobody to show a scrubber to, and the poll
        // it holds open would be a connection kept alive for nothing.
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { remote?.start() } else { remote?.stop() }
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
            remote?.stop()
            remote = nil
            // A signed-out session has no client to report progress with, so
            // the player goes with it rather than playing on silently.
            player.configure(app: nil, playback: playback)
            return
        }
        if app?.client !== client {
            app = AppModel(client: client)
            remote?.stop()
            remote = RemoteControl(client: client)
        }
        remote?.start()
        player.configure(app: app, playback: playback)
        openDebugVideo()
    }

    /// Opens a video straight from launch, so a screen that only exists during
    /// playback can be reached in a simulator without tapping through the app:
    ///
    ///     SIMCTL_CHILD_FLIMM_PLAY_VIDEO=<video id> xcrun simctl launch <device> dev.winktech.flimm
    ///
    /// `FLIMM_PLAY_FEED` / `FLIMM_PLAY_PLAYLIST` open it *in* that context,
    /// which is the only way to reach the states that depend on one — the end
    /// of a list most of all, where up next turns into suggestions.
    ///
    /// Debug builds only; the TV app has the same door (see `TVRootView`).
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

#Preview {
    ContentView()
        .environment(AuthSession(redirectURI: AppConfig.redirectURI))
}
