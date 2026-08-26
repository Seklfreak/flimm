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
                } else {
                    TVLoadingState()
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.background)
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
            player.configure(app: nil)
            return
        }
        if app?.client !== client {
            app = AppModel(client: client)
        }
        player.configure(app: app)
    }
}
