import FlimmKit
import Sentry
import SwiftUI

@main
struct FlimmTVApp: App {
    /// No authenticator here: tvOS has no browser, and the device-code screen
    /// owns the ``DeviceCodeAuthenticator`` because it is the thing that has
    /// to show the code while the poll runs.
    @State private var session = AuthSession(redirectURI: TVConfig.unusedRedirectURI, authenticator: nil)

    init() {
        // Debug builds stay quiet — local runs would drown real crash reports,
        // and the placeholder xcconfig has no DSN anyway.
        #if !DEBUG
        if let dsn = TVConfig.sentryDSN {
            SentrySDK.start { options in
                options.dsn = dsn
                options.tracesSampleRate = 1.0
                // The server URL and any bearer token would otherwise ride
                // along on breadcrumbs and request spans.
                options.sendDefaultPii = false
                // Failed-request capture defaults to the whole 5xx range,
                // which files a 502/503/504 as an app error. Those mean
                // nothing was listening — and for Flimm that is scheduled,
                // not exceptional: the media cache is a ReadWriteOnce
                // volume, so the deployment has to be `Recreate`, and every
                // release closes the door for as long as the new pod takes
                // to go ready. A player heartbeating through that window
                // reports an outage with a stack trace made entirely of
                // Sentry's own URLSession swizzle, naming no code of ours.
                // Availability belongs to the cluster's own monitoring; a
                // real 500 still lands here, and beside a Go stack in the
                // backend's project.
                options.failedRequestStatusCodes = [
                    HttpStatusCodeRange(min: 500, max: 501),
                    HttpStatusCodeRange(min: 505, max: 599),
                ]
            }
        }
        // Same rule as Sentry: local runs would only muddy the numbers, and
        // the placeholder xcconfig has no Umami values anyway.
        Analytics.configure()
        #endif
    }

    var body: some Scene {
        WindowGroup {
            TVRootView()
                .environment(session)
                .task { await session.restore() }
        }
    }
}
