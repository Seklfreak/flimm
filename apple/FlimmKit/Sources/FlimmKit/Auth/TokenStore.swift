import Foundation

/// Holds the OIDC tokens, persists them, and refreshes them on demand.
///
/// Separated from ``AuthSession`` so the API client can depend on a small,
/// non-UI actor. The rule it exists to enforce: **only a definitive refresh
/// failure ends a session.** A network error leaves the tokens in place.
public actor TokenStore: TokenProvider {
    /// Raised once when the provider answers `invalid_grant`.
    public typealias SignOutHandler = @Sendable () async -> Void
    /// Raised when refreshed tokens could not be written to the Keychain.
    public typealias PersistFailureHandler = @Sendable (any Error) async -> Void

    private let store: any SecretStore
    private let key: String
    private var client: OIDCClient?
    /// Builds the client on first need — discovery against the issuer — so a
    /// session restored from the Keychain can refresh without having signed
    /// in during this process. `signIn` still hands over a ready client.
    private var clientProvider: (@Sendable () async throws -> OIDCClient)?
    private var tokens: OIDCTokens?
    private var signOut: SignOutHandler?
    private var persistFailed: PersistFailureHandler?
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

    /// How to obtain a client when none has been handed over yet. Replacing
    /// the provider drops a client built by the previous one.
    public func configure(clientProvider: (@Sendable () async throws -> OIDCClient)?) {
        self.clientProvider = clientProvider
        self.client = nil
    }

    /// The configured client, or one built by the provider. Throws when the
    /// provider fails (typically discovery with the network down), which the
    /// API client reports as transient — never as a sign-out.
    private func resolveClient() async throws -> OIDCClient? {
        if let client { return client }
        guard let clientProvider else { return nil }
        let built = try await clientProvider()
        client = built
        return built
    }

    public func onSignOut(_ handler: SignOutHandler?) {
        self.signOut = handler
    }

    /// Called when a refresh succeeded but its tokens could not be stored.
    /// Worth hearing about: the provider rotates the refresh token and revokes
    /// the one we still have on disk, so a failed write is a session that
    /// works until the app is next launched and then cannot be recovered.
    public func onPersistFailure(_ handler: PersistFailureHandler?) {
        self.persistFailed = handler
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

    /// Renew ahead of need — on returning to the foreground, say — so the
    /// rotation happens while the app is alive and settled rather than during
    /// the burst of requests a cold launch fires, or not at all until the
    /// token has been dead for weeks. Silent about everything: a failure here
    /// was not asked for by anything on screen, and `renew()` already ends the
    /// session on the one answer that means it is over.
    public func refreshIfStale(within leeway: TimeInterval = 600) async {
        guard let tokens, tokens.isExpired(leeway: leeway) else { return }
        _ = try? await renew()
    }

    private func renew() async throws -> OIDCTokens? {
        guard let refreshToken = tokens?.refreshToken else { return nil }
        guard let client = try await resolveClient() else { return nil }

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
            do {
                try adopt(merged)
            } catch {
                await persistFailed?(error)
            }
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
