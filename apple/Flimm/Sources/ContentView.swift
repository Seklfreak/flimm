import FlimmKit
import SwiftUI

/// The root: setup → sign-in → the app.
///
/// `AuthSession` never drops to `signedOut` for a transient failure — only a
/// dead refresh token does that — so a flaky network keeps the user where they
/// are instead of throwing them back to the setup screen.
struct ContentView: View {
    @Environment(AuthSession.self) private var session

    @State private var app: AppModel?
    @State private var player = PlayerCoordinator()
    /// Shared by both shells; see ``RootShell``.
    @State private var nav = NavigationModel()

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
                if let app {
                    RootShell()
                        .environment(app)
                        .environment(player)
                        .environment(nav)
                } else {
                    LoadingState()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(Palette.background)
                }
            }
        }
        .preferredColorScheme(app?.prefs.theme.colorScheme)
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
            // A signed-out session has no client to report progress with, so
            // the player goes with it rather than playing on silently.
            player.configure(app: nil)
            return
        }
        if app?.client !== client {
            app = AppModel(client: client)
        }
        player.configure(app: app)
    }
}

#Preview {
    ContentView()
        .environment(AuthSession(redirectURI: AppConfig.redirectURI))
}
