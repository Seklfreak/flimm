import Foundation

/// The one way into a Flimm deployment.
///
/// Clients talk **only** to the Flimm backend — never to TubeArchivist, which
/// typically sits behind an auth proxy a native client cannot complete. Every
/// `/api/v1` call carries `Authorization: Bearer <jwt>`; a 401 triggers one
/// refresh and one retry.
public actor APIClient {
    /// Origin of the deployment, e.g. `https://flimm.example.com`. Paths are
    /// appended to it, so it never ends in a slash.
    public nonisolated let baseURL: URL

    private let tokens: (any TokenProvider)?
    private let session: URLSession
    private let decoder: JSONDecoder
    private let encoder: JSONEncoder

    public init(baseURL: URL, tokens: (any TokenProvider)? = nil, session: URLSession = .shared) {
        self.baseURL = APIClient.normalize(baseURL)
        self.tokens = tokens
        self.session = session
        self.decoder = FlimmCoding.decoder
        self.encoder = FlimmCoding.encoder
    }

    /// Strips a trailing slash and a trailing `/api/v1`, so a URL pasted from a
    /// browser's address bar works as well as a bare origin.
    static func normalize(_ url: URL) -> URL {
        var text = url.absoluteString
        while text.hasSuffix("/") { text.removeLast() }
        for suffix in ["/api/v1", "/api"] where text.hasSuffix(suffix) {
            text.removeLast(suffix.count)
        }
        return URL(string: text) ?? url
    }

    // MARK: - Media

    /// Resolves a media path from the API (`/media/video/id.mp4`,
    /// `/media/thumb/video/id`, a subtitle track URL) against the deployment.
    ///
    /// `from`, when given, appends `?from=<seconds>` to the resolved URL. For
    /// an HLS master this is what makes the server return a media playlist
    /// carrying `#EXT-X-START`, so `AVPlayer` begins at the resume point and
    /// fetches the resume segment first instead of blocking on segment 0.
    public nonisolated func mediaURL(_ path: String, from: Int? = nil) -> URL? {
        let resolved: URL?
        if let absolute = URL(string: path), absolute.scheme != nil {
            resolved = absolute
        } else {
            resolved = URL(string: path, relativeTo: baseURL)?.absoluteURL
        }
        guard let url = resolved else { return nil }
        guard let from else { return url }
        guard var comps = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return url }
        comps.queryItems = (comps.queryItems ?? []) + [URLQueryItem(name: "from", value: String(from))]
        return comps.url ?? url
    }

    /// The `AVURLAsset` option key that carries ``mediaHeaders()``.
    ///
    /// AVFoundation publishes no Swift symbol for it, so the raw string is the
    /// only way to attach the bearer token to an asset's requests — including
    /// the byte-range requests seeking makes. It lives here rather than in a
    /// target so the iOS and tvOS players cannot drift on the spelling.
    public static let assetHTTPHeaderFieldsKey = "AVURLAssetHTTPHeaderFieldsKey"

    /// Headers for ``assetHTTPHeaderFieldsKey``.
    ///
    /// `/media/*` accepts a Bearer header *or* the signed `flimm_media`
    /// cookie; the cookie exists only because a browser `<video>` cannot set
    /// headers. Native clients use the header and ignore the cookie path
    /// entirely — including on byte-range requests, which must carry it too.
    public func mediaHeaders() async throws -> [String: String] {
        guard let token = try await tokens?.accessToken() else { return [:] }
        return ["Authorization": "Bearer \(token)"]
    }

    // MARK: - Meta / session

    /// Unauthenticated. Validates a server URL and configures the auth flow.
    public func config() async throws -> ServerConfig {
        try await get("/config", authenticated: false)
    }

    /// Unauthenticated liveness probe; `ta` reports TubeArchivist reachability.
    public func health() async throws -> ServerHealth {
        try await get("/healthz", authenticated: false)
    }

    public func me() async throws -> Me {
        try await get("/me")
    }

    public func updatePrefs(_ patch: PrefsPatch) async throws -> Prefs {
        try await send(.patch, "/me/prefs", body: patch)
    }

    // MARK: - Request plumbing

    enum Method: String {
        case get = "GET"
        case post = "POST"
        case put = "PUT"
        case patch = "PATCH"
        case delete = "DELETE"
    }

    /// `GET` on a path below `/api/v1`.
    func get<T: Decodable>(_ path: String, query: [URLQueryItem] = [], authenticated: Bool = true) async throws -> T {
        try await request(.get, path: "/api/v1" + path, query: query, authenticated: authenticated)
    }

    /// A body-carrying call on a path below `/api/v1`.
    func send<T: Decodable>(_ method: Method, _ path: String, query: [URLQueryItem] = [], body: (any Encodable)? = nil) async throws -> T {
        try await request(method, path: "/api/v1" + path, query: query, body: body)
    }

    /// A call below `/api/v1` whose response is discarded (204, mostly).
    func discard(_ method: Method, _ path: String, query: [URLQueryItem] = [], body: (any Encodable)? = nil) async throws {
        _ = try await data(method, path: "/api/v1" + path, query: query, body: body, authenticated: true)
    }

    private func request<T: Decodable>(
        _ method: Method,
        path: String,
        query: [URLQueryItem] = [],
        body: (any Encodable)? = nil,
        authenticated: Bool = true
    ) async throws -> T {
        let payload = try await data(method, path: path, query: query, body: body, authenticated: authenticated)
        do {
            return try decoder.decode(T.self, from: payload)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }

    private func data(
        _ method: Method,
        path: String,
        query: [URLQueryItem],
        body: (any Encodable)?,
        authenticated: Bool
    ) async throws -> Data {
        guard var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else {
            throw APIError.invalidURL
        }
        components.path = (components.path.isEmpty ? "" : components.path) + path
        components.queryItems = query.isEmpty ? nil : query
        guard let url = components.url else { throw APIError.invalidURL }

        var request = URLRequest(url: url)
        request.httpMethod = method.rawValue
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            request.httpBody = try encoder.encode(body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        let (payload, response) = try await perform(request, authenticated: authenticated)
        guard (200..<300).contains(response.statusCode) else {
            throw APIError.fromStatus(response.statusCode, message: Self.errorMessage(from: payload))
        }
        // 204 and an empty 200 body both decode as `null`, which satisfies an
        // optional or Void result without a separate code path.
        return payload.isEmpty ? Data("null".utf8) : payload
    }

    /// Sends the request, and on a 401 refreshes once and retries once.
    private func perform(_ request: URLRequest, authenticated: Bool) async throws -> (Data, HTTPURLResponse) {
        var attempt = request
        if authenticated, let token = try await currentToken() {
            attempt.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        var result = try await roundTrip(attempt)
        guard authenticated, result.1.statusCode == 401 else { return result }

        let refreshed: String?
        do {
            refreshed = try await tokens?.refreshAccessToken()
        } catch let error as OIDCError where error == .invalidGrant {
            throw APIError.unauthorized
        } catch {
            // The refresh itself failed for a reason that is not an answer
            // (offline, DNS, a 502 at the provider). Reporting this as
            // "unauthorized" is what wedges a signed-in user on a flaky
            // network, so it stays transient.
            throw APIError.transport(String(describing: error))
        }
        guard let refreshed else { return result }

        var retry = request
        retry.setValue("Bearer \(refreshed)", forHTTPHeaderField: "Authorization")
        result = try await roundTrip(retry)
        return result
    }

    private func currentToken() async throws -> String? {
        do {
            return try await tokens?.accessToken()
        } catch let error as OIDCError where error == .invalidGrant {
            throw APIError.unauthorized
        } catch {
            throw APIError.transport(String(describing: error))
        }
    }

    private func roundTrip(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        do {
            let (data, response) = try await session.data(for: request)
            guard let http = response as? HTTPURLResponse else {
                throw APIError.transport("not an HTTP response")
            }
            return (data, http)
        } catch let error as APIError {
            throw error
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
    }

    /// Errors come back as `{ "error": "message" }`.
    private static func errorMessage(from data: Data) -> String {
        struct Envelope: Decodable { let error: String? }
        guard let envelope = try? JSONDecoder().decode(Envelope.self, from: data) else { return "" }
        return envelope.error ?? ""
    }
}
