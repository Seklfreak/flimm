import FlimmKit
import Sentry
import SwiftUI

@main
struct FlimmApp: App {
    @State private var session = AuthSession(redirectURI: AppConfig.redirectURI)

    init() {
        // Debug builds stay quiet — local runs would drown real crash reports,
        // and the placeholder xcconfig has no DSN anyway.
        #if !DEBUG
        if let dsn = AppConfig.sentryDSN {
            SentrySDK.start { options in
                options.dsn = dsn
                options.tracesSampleRate = 1.0
                // The server URL and any bearer token would otherwise ride
                // along on breadcrumbs and request spans.
                options.sendDefaultPii = false
            }
        }
        // Same rule as Sentry: local runs would only muddy the numbers, and
        // the placeholder xcconfig has no Umami values anyway.
        Analytics.configure()
        #endif
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(session)
                .task { await session.restore() }
        }
    }
}
