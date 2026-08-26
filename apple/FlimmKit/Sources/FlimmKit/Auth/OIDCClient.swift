import Foundation

/// The subset of the discovery document a client needs.
public struct OIDCConfiguration: Sendable, Hashable, Codable {
    public let issuer: String
    public let authorizationEndpoint: URL
    public let tokenEndpoint: URL
    public let endSessionEndpoint: URL?
    /// RFC 8628. Absent on providers that do not offer the device grant —
    /// which is a hard stop on Apple TV, where there is no browser to fall
    /// back to. See ``OIDCError/deviceFlowUnsupported``.
    public let deviceAuthorizationEndpoint: URL?

    public init(
        issuer: String,
        authorizationEndpoint: URL,
        tokenEndpoint: URL,
        endSessionEndpoint: URL? = nil,
        deviceAuthorizationEndpoint: URL? = nil
    ) {
        self.issuer = issuer
        self.authorizationEndpoint = authorizationEndpoint
        self.tokenEndpoint = tokenEndpoint
        self.endSessionEndpoint = endSessionEndpoint
        self.deviceAuthorizationEndpoint = deviceAuthorizationEndpoint
    }

    private enum CodingKeys: String, CodingKey {
        case issuer
        case authorizationEndpoint = "authorization_endpoint"
        case tokenEndpoint = "token_endpoint"
        case endSessionEndpoint = "end_session_endpoint"
        case deviceAuthorizationEndpoint = "device_authorization_endpoint"
    }
}

public struct OIDCTokens: Sendable, Hashable, Codable {
    public let accessToken: String
    /// Absent unless the provider grants `offline_access`. Without it the app
    /// silently logs out when the access token expires.
    public let refreshToken: String?
    public let idToken: String?
    public let tokenType: String
    public let expiresAt: Date

    public init(accessToken: String, refreshToken: String?, idToken: String?, tokenType: String = "Bearer", expiresAt: Date) {
        self.accessToken = accessToken
        self.refreshToken = refreshToken
        self.idToken = idToken
        self.tokenType = tokenType
        self.expiresAt = expiresAt
    }

    /// A minute of slack, so a token isn't sent while it expires in flight.
    public func isExpired(now: Date = Date(), leeway: TimeInterval = 60) -> Bool {
        expiresAt.timeIntervalSince(now) <= leeway
    }
}

public enum OIDCError: Error, Sendable, Equatable {
    case discoveryFailed(String)
    case invalidConfiguration
    /// The provider redirected back with `error=…` — including
    /// `access_denied` when the user cancelled.
    case authorizationFailed(String)
    /// The callback URL carried a different `state` than we sent.
    case stateMismatch
    case missingCode
    case tokenExchangeFailed(String)
    /// The one definitive answer: the refresh token is dead and the session is
    /// over. Every other failure is transient and must not sign a user out.
    case invalidGrant
    case network(String)
    /// The provider's discovery document has no `device_authorization_endpoint`.
    /// tvOS cannot fall back to a browser, so this ends the flow.
    case deviceFlowUnsupported
    /// The user code timed out before anyone approved it (`expired_token`).
    case deviceCodeExpired

    public var errorMessage: String {
        switch self {
        case .discoveryFailed(let m): "Couldn't read the sign-in provider's configuration. \(m)"
        case .invalidConfiguration: "The sign-in provider's configuration is incomplete."
        case .authorizationFailed(let m): m
        case .stateMismatch: "The sign-in response didn't match the request."
        case .missingCode: "The sign-in provider didn't return an authorization code."
        case .tokenExchangeFailed(let m): m
        case .invalidGrant: "The session expired. Sign in again."
        case .network(let m): m
        case .deviceFlowUnsupported:
            """
            This sign-in provider doesn't support the device authorization \
            grant (RFC 8628), which is the only way to sign in on Apple TV. \
            Enable it for the same OIDC client id and try again.
            """
        case .deviceCodeExpired: "The code expired before it was approved. Start again."
        }
    }
}

extension OIDCError: LocalizedError {
    public var errorDescription: String? { errorMessage }
}

/// Authorization Code + PKCE against the issuer the *server* named. The user
/// never types a provider URL — `GET /api/v1/config` supplies it.
public actor OIDCClient {
    /// `offline_access` is what earns a refresh token. Without it the app
    /// logs out as soon as the access token expires.
    public static let defaultScopes = ["openid", "profile", "email", "offline_access"]

    public nonisolated let configuration: OIDCConfiguration
    public nonisolated let clientID: String
    public nonisolated let redirectURI: URL
    public nonisolated let scopes: [String]
    let session: URLSession

    public init(
        configuration: OIDCConfiguration,
        clientID: String,
        redirectURI: URL,
        scopes: [String] = OIDCClient.defaultScopes,
        session: URLSession = .shared
    ) {
        self.configuration = configuration
        self.clientID = clientID
        self.redirectURI = redirectURI
        self.scopes = scopes
        self.session = session
    }

    /// Fetches `<issuer>/.well-known/openid-configuration`.
    public static func discover(issuer: URL, session: URLSession = .shared) async throws -> OIDCConfiguration {
        var text = issuer.absoluteString
        while text.hasSuffix("/") { text.removeLast() }
        guard let url = URL(string: text + "/.well-known/openid-configuration") else {
            throw OIDCError.invalidConfiguration
        }
        do {
            let (data, response) = try await session.data(from: url)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                throw OIDCError.discoveryFailed("The provider returned an error.")
            }
            return try JSONDecoder().decode(OIDCConfiguration.self, from: data)
        } catch let error as OIDCError {
            throw error
        } catch let error as DecodingError {
            throw OIDCError.discoveryFailed(String(describing: error))
        } catch {
            throw OIDCError.network(error.localizedDescription)
        }
    }

    /// The URL to open in the browser. `state` and the PKCE verifier must be
    /// held until the callback arrives.
    public nonisolated func authorizationURL(pkce: PKCE, state: String) throws -> URL {
        guard var components = URLComponents(url: configuration.authorizationEndpoint, resolvingAgainstBaseURL: false) else {
            throw OIDCError.invalidConfiguration
        }
        var items = components.queryItems ?? []
        items.append(contentsOf: [
            URLQueryItem(name: "response_type", value: "code"),
            URLQueryItem(name: "client_id", value: clientID),
            URLQueryItem(name: "redirect_uri", value: redirectURI.absoluteString),
            URLQueryItem(name: "scope", value: scopes.joined(separator: " ")),
            URLQueryItem(name: "state", value: state),
            URLQueryItem(name: "code_challenge", value: pkce.challenge),
            URLQueryItem(name: "code_challenge_method", value: PKCE.method)
        ])
        components.queryItems = items
        guard let url = components.url else { throw OIDCError.invalidConfiguration }
        return url
    }

    /// Pulls the `code` out of the callback URL after checking `state`.
    public nonisolated func code(from callback: URL, state: String) throws -> String {
        let items = URLComponents(url: callback, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func value(_ name: String) -> String? { items.first { $0.name == name }?.value }

        if let error = value("error") {
            throw OIDCError.authorizationFailed(value("error_description") ?? error)
        }
        guard value("state") == state else { throw OIDCError.stateMismatch }
        guard let code = value("code"), !code.isEmpty else { throw OIDCError.missingCode }
        return code
    }

    public func exchange(code: String, pkce: PKCE) async throws -> OIDCTokens {
        try await token(form: [
            "grant_type": "authorization_code",
            "code": code,
            "client_id": clientID,
            "redirect_uri": redirectURI.absoluteString,
            "code_verifier": pkce.verifier
        ])
    }

    /// Providers may or may not rotate the refresh token; when the response
    /// omits one, the caller keeps the old one.
    public func refresh(refreshToken: String) async throws -> OIDCTokens {
        try await token(form: [
            "grant_type": "refresh_token",
            "refresh_token": refreshToken,
            "client_id": clientID
        ])
    }

    /// What the token endpoint answered.
    ///
    /// The device grant needs the provider's `error` *code* rather than a
    /// thrown message — `authorization_pending` and `slow_down` are normal
    /// steps of a successful sign-in, not failures.
    enum TokenOutcome {
        case tokens(OIDCTokens)
        case failure(code: String, description: String?)
    }

    private func token(form: [String: String]) async throws -> OIDCTokens {
        switch try await postToken(form: form) {
        case .tokens(let tokens):
            return tokens
        case .failure(let code, let description):
            // `invalid_grant` is the provider saying the refresh token is
            // dead. Everything else — 500s, rate limits, a proxy in the way —
            // is transient and must not end the session.
            if code == "invalid_grant" { throw OIDCError.invalidGrant }
            throw OIDCError.tokenExchangeFailed(description ?? code)
        }
    }

    func postToken(form: [String: String]) async throws -> TokenOutcome {
        var request = URLRequest(url: configuration.tokenEndpoint)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.httpBody = Data(OIDCClient.formEncode(form).utf8)

        let data: Data
        let http: HTTPURLResponse
        do {
            let (payload, response) = try await session.data(for: request)
            guard let status = response as? HTTPURLResponse else { throw OIDCError.network("not an HTTP response") }
            data = payload
            http = status
        } catch let error as OIDCError {
            throw error
        } catch {
            throw OIDCError.network(error.localizedDescription)
        }

        guard (200..<300).contains(http.statusCode) else {
            let failure = try? JSONDecoder().decode(TokenErrorResponse.self, from: data)
            return .failure(
                code: failure?.error ?? "http_\(http.statusCode)",
                description: failure?.errorDescription ?? failure?.error ?? "HTTP \(http.statusCode)"
            )
        }

        do {
            let response = try JSONDecoder().decode(TokenResponse.self, from: data)
            return .tokens(OIDCTokens(
                accessToken: response.accessToken,
                refreshToken: response.refreshToken,
                idToken: response.idToken,
                tokenType: response.tokenType ?? "Bearer",
                expiresAt: Date().addingTimeInterval(TimeInterval(response.expiresIn ?? 3600))
            ))
        } catch {
            throw OIDCError.tokenExchangeFailed(String(describing: error))
        }
    }

    static func formEncode(_ form: [String: String]) -> String {
        var allowed = CharacterSet.alphanumerics
        allowed.insert(charactersIn: "-._~")
        return form
            .sorted { $0.key < $1.key }
            .map { key, value in
                let name = key.addingPercentEncoding(withAllowedCharacters: allowed) ?? key
                let encoded = value.addingPercentEncoding(withAllowedCharacters: allowed) ?? value
                return "\(name)=\(encoded)"
            }
            .joined(separator: "&")
    }
}

private struct TokenResponse: Decodable {
    let accessToken: String
    let refreshToken: String?
    let idToken: String?
    let tokenType: String?
    let expiresIn: Int?

    enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case refreshToken = "refresh_token"
        case idToken = "id_token"
        case tokenType = "token_type"
        case expiresIn = "expires_in"
    }
}

private struct TokenErrorResponse: Decodable {
    let error: String?
    let errorDescription: String?

    enum CodingKeys: String, CodingKey {
        case error
        case errorDescription = "error_description"
    }
}
