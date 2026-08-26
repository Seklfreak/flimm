import Foundation

/// Build-time configuration, injected through `Config/Secrets.xcconfig` into
/// the Info.plist. Nothing here may be a real value in the repository — see
/// the "public and generic" rule in the root `CLAUDE.md`.
enum AppConfig {
    /// Optional — empty or missing disables crash reporting entirely, which is
    /// what CI and simulator builds run with.
    static var sentryDSN: String? {
        guard let dsn = Bundle.main.object(forInfoDictionaryKey: "SENTRY_DSN") as? String, !dsn.isEmpty else {
            return nil
        }
        return dsn
    }

    /// The native OIDC redirect URI. The provider must allow this exact value
    /// on the client — a deployment-side step the app names verbatim when
    /// sign-in fails.
    static let redirectURI = URL(string: "dev.winktech.flimm://auth")!

    /// "1.0.0 (12)" — shown in Settings and useful in a bug report.
    static var displayVersion: String {
        let short = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "—"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "—"
        return "\(short) (\(build))"
    }
}
