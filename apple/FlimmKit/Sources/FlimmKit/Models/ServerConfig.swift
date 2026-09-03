import Foundation

/// `GET /api/v1/config` — unauthenticated, and the only thing a native client
/// needs beyond the server URL: it both validates the URL the user typed and
/// configures the OIDC flow.
public struct ServerConfig: Codable, Sendable, Hashable {
    public let appName: String
    public let oidcIssuer: String
    public let oidcClientId: String
    public let version: String
    /// The server runs with `AUTH_DISABLED=true`: there is nothing to sign in
    /// to, and every request is the same fixed user.
    ///
    /// The server says this outright rather than leaving it to be inferred
    /// from empty OIDC fields, because the two cases are opposites: a server
    /// deliberately running open is one to connect to, while a server that
    /// wants auth but publishes no issuer is broken and must not be.
    public let authDisabled: Bool
    /// The server runs with `ANALYTICS_DISABLED=true`: this deployment is not
    /// to be reported on, whatever analytics endpoint the app was built with.
    /// See ``Analytics/apply(_:)``.
    public let analyticsDisabled: Bool
    /// The server has an APNs key: a feed's notify flag reaches a phone.
    /// Without it the editor does not offer the switch at all — a control
    /// that does nothing is worse than none.
    public let pushEnabled: Bool

    /// `false` when the deployment runs with `AUTH_DISABLED=true`, or is
    /// otherwise missing OIDC settings.
    public var hasOIDC: Bool {
        !oidcIssuer.isEmpty && !oidcClientId.isEmpty && issuerURL != nil
    }

    /// Whether a client can use this server at all: it either has a sign-in
    /// provider, or it has no sign-in.
    public var isUsable: Bool { hasOIDC || authDisabled }

    public var issuerURL: URL? {
        guard let url = URL(string: oidcIssuer), url.scheme != nil, url.host != nil else { return nil }
        return url
    }

    public init(
        appName: String = "Flimm",
        oidcIssuer: String = "",
        oidcClientId: String = "",
        version: String = "",
        authDisabled: Bool = false,
        analyticsDisabled: Bool = false,
        pushEnabled: Bool = false
    ) {
        self.appName = appName
        self.oidcIssuer = oidcIssuer
        self.oidcClientId = oidcClientId
        self.version = version
        self.authDisabled = authDisabled
        self.analyticsDisabled = analyticsDisabled
        self.pushEnabled = pushEnabled
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        appName = try c.decode(.appName, or: "Flimm")
        oidcIssuer = try c.decode(.oidcIssuer, or: "")
        oidcClientId = try c.decode(.oidcClientId, or: "")
        version = try c.decode(.version, or: "")
        authDisabled = try c.decode(.authDisabled, or: false)
        analyticsDisabled = try c.decode(.analyticsDisabled, or: false)
        pushEnabled = try c.decode(.pushEnabled, or: false)
    }
}

/// `GET /healthz` — unauthenticated. `ta` reports TubeArchivist reachability.
public struct ServerHealth: Codable, Sendable, Hashable {
    public let status: String
    public let ta: String?

    public init(status: String, ta: String? = nil) {
        self.status = status
        self.ta = ta
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        status = try c.decode(.status, or: "ok")
        ta = try c.decodeIfPresent(String.self, forKey: .ta)
    }
}
