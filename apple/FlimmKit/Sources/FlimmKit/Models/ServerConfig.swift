import Foundation

/// `GET /api/v1/config` — unauthenticated, and the only thing a native client
/// needs beyond the server URL: it both validates the URL the user typed and
/// configures the OIDC flow.
public struct ServerConfig: Codable, Sendable, Hashable {
    public let appName: String
    public let oidcIssuer: String
    public let oidcClientId: String
    public let version: String

    /// `false` when the deployment runs with `AUTH_DISABLED=true`, or is
    /// otherwise missing OIDC settings — a native client cannot sign in.
    public var hasOIDC: Bool {
        !oidcIssuer.isEmpty && !oidcClientId.isEmpty && issuerURL != nil
    }

    public var issuerURL: URL? {
        guard let url = URL(string: oidcIssuer), url.scheme != nil, url.host != nil else { return nil }
        return url
    }

    public init(appName: String = "Flimm", oidcIssuer: String = "", oidcClientId: String = "", version: String = "") {
        self.appName = appName
        self.oidcIssuer = oidcIssuer
        self.oidcClientId = oidcClientId
        self.version = version
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        appName = try c.decode(.appName, or: "Flimm")
        oidcIssuer = try c.decode(.oidcIssuer, or: "")
        oidcClientId = try c.decode(.oidcClientId, or: "")
        version = try c.decode(.version, or: "")
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
