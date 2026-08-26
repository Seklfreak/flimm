import Foundation

/// Supplies the bearer token the API client puts on every request, and knows
/// how to get a fresh one.
///
/// Kept a protocol so ``APIClient`` has no opinion about where tokens come
/// from — the app plugs in ``TokenStore``, tests plug in a stub.
public protocol TokenProvider: Sendable {
    /// The current access token, refreshing it first if it is known to have
    /// expired. `nil` when there is no session.
    func accessToken() async throws -> String?

    /// Force a refresh after a 401 and return the new token, or `nil` when the
    /// session cannot be renewed.
    ///
    /// Implementations must distinguish a definitive failure (the provider
    /// answered `invalid_grant`) from a transient one (the network is down):
    /// only the former ends a session.
    func refreshAccessToken() async throws -> String?
}

/// A fixed token — useful for `AUTH_DISABLED=true` deployments and tests.
public struct StaticTokenProvider: TokenProvider {
    private let token: String?

    public init(_ token: String?) {
        self.token = token
    }

    public func accessToken() async throws -> String? { token }
    public func refreshAccessToken() async throws -> String? { nil }
}
