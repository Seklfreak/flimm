import Foundation

/// How a platform turns "we know which provider" into tokens.
///
/// There are two strategies and they are not interchangeable per platform:
/// iPhone and iPad open a browser (``BrowserAuthenticator``), Apple TV has no
/// browser at all and runs the device authorization grant
/// (``DeviceCodeAuthenticator``). ``AuthSession`` holds one of them and knows
/// about neither.
public protocol Authenticating: Sendable {
    /// Runs the user-facing half of sign-in against an already-discovered
    /// provider and returns the tokens.
    func authorize(using client: OIDCClient) async throws -> OIDCTokens
}

/// Authorization Code + PKCE through a browser: the iOS and iPadOS path.
public struct BrowserAuthenticator: Authenticating {
    private let web: any WebAuthenticating

    public init(web: any WebAuthenticating) {
        self.web = web
    }

    public func authorize(using client: OIDCClient) async throws -> OIDCTokens {
        let pkce = PKCE()
        let state = PKCE.randomState()
        let url = try client.authorizationURL(pkce: pkce, state: state)
        let callback = try await web.authenticate(url: url, callbackScheme: client.redirectURI.scheme ?? "")
        let code = try client.code(from: callback, state: state)
        return try await client.exchange(code: code, pkce: pkce)
    }
}

/// The device authorization grant (RFC 8628): the tvOS path.
///
/// `present` is called once, as soon as the provider has issued a code, so the
/// screen can show the verification URL, the user code and its QR code while
/// this keeps polling in the background.
public struct DeviceCodeAuthenticator: Authenticating {
    public typealias Presenter = @Sendable (DeviceAuthorization) async -> Void

    private let present: Presenter
    private let slowDownIncrement: Duration

    public init(slowDownIncrement: Duration = .seconds(5), present: @escaping Presenter) {
        self.slowDownIncrement = slowDownIncrement
        self.present = present
    }

    public func authorize(using client: OIDCClient) async throws -> OIDCTokens {
        let authorization = try await client.deviceAuthorize()
        await present(authorization)
        return try await client.pollForDeviceToken(authorization, slowDownIncrement: slowDownIncrement)
    }
}
