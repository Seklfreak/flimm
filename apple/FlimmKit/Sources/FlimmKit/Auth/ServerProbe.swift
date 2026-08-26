import Foundation

/// A server URL that answered `/api/v1/config` with something usable.
public struct FlimmServer: Sendable, Hashable, Codable {
    public let baseURL: URL
    public let config: ServerConfig

    public init(baseURL: URL, config: ServerConfig) {
        self.baseURL = baseURL
        self.config = config
    }
}

/// Why a server URL did not work. The three failures need visibly different
/// messages: one is the user's typing, one is the wrong host, and one is a
/// deployment that has to be reconfigured before any client can sign in.
public enum ServerProbeError: Error, Sendable, Equatable {
    /// Not a URL at all.
    case invalidURL
    /// Nothing answered: offline, wrong host, or a server that is deliberately
    /// not on the public internet. Retrying later is reasonable.
    case unreachable(String)
    /// Something answered, but it is not a Flimm backend.
    case notAFlimmServer
    /// A Flimm backend that publishes no OIDC issuer or client id — typically
    /// running with `AUTH_DISABLED=true`. A native client cannot sign in until
    /// the deployment is configured.
    case oidcNotConfigured

    public var errorMessage: String {
        switch self {
        case .invalidURL:
            "That doesn't look like a web address."
        case .unreachable(let detail):
            "Couldn't reach the server. \(detail)"
        case .notAFlimmServer:
            "That address answered, but it isn't a Flimm server."
        case .oidcNotConfigured:
            "That Flimm server has no sign-in provider configured, so this app can't sign in to it."
        }
    }
}

extension ServerProbeError: LocalizedError {
    public var errorDescription: String? { errorMessage }
}

/// Turns whatever the user typed into a working base URL, or into one of three
/// distinguishable failures. A friendly failure here is most of the setup UX.
public struct ServerProbe: Sendable {
    private let session: URLSession

    public init(session: URLSession = .shared) {
        self.session = session
    }

    /// Accepts `flimm.example.com`, `https://flimm.example.com/`, and a URL
    /// copied out of the browser with `/api/v1` still on the end.
    public static func normalize(_ raw: String) -> URL? {
        var text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }
        if !text.contains("://") { text = "https://" + text }
        guard let url = URL(string: text), let scheme = url.scheme, url.host != nil else { return nil }
        guard scheme == "https" || scheme == "http" else { return nil }
        return APIClient.normalize(url)
    }

    public func probe(_ raw: String) async throws -> FlimmServer {
        guard let baseURL = ServerProbe.normalize(raw) else { throw ServerProbeError.invalidURL }
        return try await probe(baseURL: baseURL)
    }

    public func probe(baseURL: URL) async throws -> FlimmServer {
        let client = APIClient(baseURL: baseURL, tokens: nil, session: session)
        let config: ServerConfig
        do {
            config = try await client.config()
        } catch let error as APIError {
            switch error {
            case .transport(let detail):
                throw ServerProbeError.unreachable(detail)
            case .decoding, .notFound, .http:
                // A 404, or HTML where JSON was expected: a web server, but
                // not this one.
                throw ServerProbeError.notAFlimmServer
            default:
                throw ServerProbeError.notAFlimmServer
            }
        }

        // `app_name` always has a value (it defaults to "Flimm" server-side),
        // so an empty one means we decoded something that merely looked like
        // the right JSON.
        guard !config.appName.isEmpty else { throw ServerProbeError.notAFlimmServer }
        guard config.hasOIDC else { throw ServerProbeError.oidcNotConfigured }
        return FlimmServer(baseURL: baseURL, config: config)
    }
}
