import Foundation

/// Build-time configuration, injected through `Config/Secrets.xcconfig` into
/// the Info.plist. Nothing here may be a real value in the repository — see
/// the "public and generic" rule in the root `CLAUDE.md`.
enum TVConfig {
    /// Optional — empty or missing disables crash reporting entirely, which is
    /// what CI and simulator builds run with.
    static var sentryDSN: String? {
        guard let dsn = Bundle.main.object(forInfoDictionaryKey: "SENTRY_DSN") as? String, !dsn.isEmpty else {
            return nil
        }
        return dsn
    }

    /// `AuthSession` is built around a redirect URI because the phone app's
    /// browser flow needs one. Apple TV signs in with the device authorization
    /// grant, where no redirect ever comes back to the app, so this value is
    /// never sent to a provider and never has to be allow-listed on the
    /// client. It exists only to satisfy the initialiser.
    static let unusedRedirectURI = URL(string: "dev.winktech.flimm.tv://auth")!

    /// "1.0.0 (12)" — shown in Settings and useful in a bug report.
    static var displayVersion: String {
        let short = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "—"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "—"
        return "\(short) (\(build))"
    }
}

/// Sizes and spacings the whole TV layout is measured in. A 10-foot interface
/// is not the phone's numbers scaled up — the type is larger relative to the
/// artwork and everything is further apart, because focus needs somewhere to
/// travel.
enum TVMetrics {
    /// The overscan-safe margin. tvOS clips the outer ~60pt on some displays.
    static let margin: CGFloat = 60
    static let gridSpacing: CGFloat = 44
    /// Room for a focused card to grow into without clipping its neighbours.
    static let focusPadding: CGFloat = 30
}
