import Foundation

/// Holds the OIDC tokens, persists them, and refreshes them on demand.
///
/// Separated from ``AuthSession`` so the API client can depend on a small,
/// non-UI actor. The rule it exists to enforce: **only a definitive refresh
/// failure ends a session.** A network error leaves the tokens in place.
public actor TokenStore: TokenProvider {
    /// Raised once when the provider answers `invalid_grant`.
    public typealias SignOutHandler = @Sendable () async -> Void

    private let store: any SecretStore
    private let key: String
    private var client: OIDCClient?
    private var tokens: OIDCTokens?
    private var signOut: SignOutHandler?
    /// Concurrent 401s must produce one refresh, not one each.
    private var inFlight: Task<OIDCTokens, any Error>?

    public init(store: any SecretStore, key: String = "oidc-tokens") {
        self.store = store
        self.key = key
    }

    /// Load whatever a previous launch left behind. Never throws: a Keychain
    /// that cannot be read is a signed-out state, not a crash.
    public func load() -> OIDCTokens? {
        guard let data = try? store.read(key),
              let decoded = try? JSONDecoder().decode(OIDCTokens.self, from: data) else { return nil }
        tokens = decoded
        return decoded
    }

    public func adopt(_ tokens: OIDCTokens) throws {
        self.tokens = tokens
        try store.write(key, JSONEncoder().encode(tokens))
    }

    public func clear() {
        tokens = nil
        inFlight?.cancel()
        inFlight = nil
        try? store.delete(key)
    }

    public func configure(client: OIDCClient?) {
        self.client = client
    }

    public func onSignOut(_ handler: SignOutHandler?) {
        self.signOut = handler
    }

    public var hasSession: Bool { tokens != nil }

    public var current: OIDCTokens? { tokens }

    // MARK: - TokenProvider

    public func accessToken() async throws -> String? {
        guard let tokens else { return nil }
        guard tokens.isExpired() else { return tokens.accessToken }
        return try await renew()?.accessToken
    }

    public func refreshAccessToken() async throws -> String? {
        try await renew()?.accessToken
    }

    private func renew() async throws -> OIDCTokens? {
        guard let refreshToken = tokens?.refreshToken, let client else { return nil }

        if let inFlight { return try await inFlight.value }

        let task = Task { [client] () throws -> OIDCTokens in
            try await client.refresh(refreshToken: refreshToken)
        }
        inFlight = task
        defer { inFlight = nil }

        do {
            let refreshed = try await task.value
            // Providers that don't rotate refresh tokens omit the field; the
            // old one stays valid, so keep it rather than losing the session.
            let merged = OIDCTokens(
                accessToken: refreshed.accessToken,
                refreshToken: refreshed.refreshToken ?? refreshToken,
                idToken: refreshed.idToken ?? tokens?.idToken,
                tokenType: refreshed.tokenType,
                expiresAt: refreshed.expiresAt
            )
            try? adopt(merged)
            return merged
        } catch let error as OIDCError where error == .invalidGrant {
            clear()
            await signOut?()
            throw error
        }
        // Any other error propagates with the tokens untouched, so the next
        // attempt — on a working network — succeeds.
    }
}
