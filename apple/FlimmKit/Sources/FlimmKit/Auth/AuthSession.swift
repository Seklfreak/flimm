import Foundation
import Observation

/// The app's session: which server, whether we are signed in, and the
/// ``APIClient`` to use once we are.
///
/// Two rules shape it:
///
/// - **The server URL is the only thing the user types.** `/api/v1/config`
///   supplies the issuer and client id, so there is no provider setup screen.
/// - **Never sign out on a transient error.** A deployment may be off the
///   public internet; a failed request means "try again", not "log out". Only
///   an `invalid_grant` from the provider ends a session.
@MainActor
@Observable
public final class AuthSession {
    public enum State: Sendable, Equatable {
        /// Reading what the last launch stored.
        case loading
        /// No server URL yet — show the setup screen.
        case needsServer
        /// Server known, no valid session.
        case signedOut
        /// Ready; ``AuthSession/client`` is usable.
        case signedIn
    }

    public private(set) var state: State = .loading
    public private(set) var server: FlimmServer?
    /// The last failure, for display. Cleared by the next successful step.
    public private(set) var lastError: String?
    /// Non-nil from ``State/signedOut`` onwards — some screens (a server
    /// health check) can use it before sign-in.
    public private(set) var client: APIClient?

    /// Whether this server has a sign-in at all. `false` for a server running
    /// `AUTH_DISABLED=true`: there is no provider, no token and no sign-out —
    /// the only way "back" is to leave the server.
    public var requiresSignIn: Bool { !(server?.config.authDisabled ?? false) }

    private let secrets: any SecretStore
    private let defaults: UserDefaults
    private let session: URLSession
    private let authenticator: (any Authenticating)?
    private let redirectURI: URL
    private let tokenStore: TokenStore
    private var oidc: OIDCClient?

    private static let serverKey = "flimm.server"
    /// Sent as the bearer token to a server running without auth. Any
    /// non-empty value does; this one says why it exists in a log line.
    static let authDisabledToken = "auth-disabled"

    public init(
        redirectURI: URL,
        secrets: any SecretStore = KeychainStore(),
        defaults: UserDefaults = .standard,
        session: URLSession = .shared,
        authenticator: (any Authenticating)? = AuthSession.defaultAuthenticator()
    ) {
        self.redirectURI = redirectURI
        self.secrets = secrets
        self.defaults = defaults
        self.session = session
        self.authenticator = authenticator
        self.tokenStore = TokenStore(store: secrets)
    }

    /// The browser strategy where there is a browser. tvOS gets `nil` and the
    /// app supplies a ``DeviceCodeAuthenticator`` instead — it needs to show
    /// the code, so it has to own the strategy.
    public static func defaultAuthenticator() -> (any Authenticating)? {
        #if os(iOS) || os(visionOS)
        BrowserAuthenticator(web: WebAuthenticationSessionAuthenticator())
        #else
        nil
        #endif
    }

    /// The scheme half of the redirect URI, e.g. `dev.winktech.flimm`.
    public var callbackScheme: String { redirectURI.scheme ?? "" }

    // MARK: - Lifecycle

    /// Restore the last session. Called once at launch.
    ///
    /// A stored server that cannot be re-probed right now keeps its stored
    /// config: the app comes up signed in and offline rather than throwing the
    /// user back to the setup screen because the VPN isn't up yet.
    public func restore() async {
        if let stored = storedServer() {
            adopt(server: stored)
            if stored.config.authDisabled {
                state = .signedIn
                return
            }
            let tokens = await tokenStore.load()
            state = tokens == nil ? .signedOut : .signedIn
        } else {
            state = .needsServer
        }
    }

    /// Validate and remember a server URL. Throws ``ServerProbeError``.
    public func connect(to raw: String) async throws {
        lastError = nil
        do {
            let probed = try await ServerProbe(session: session).probe(raw)
            storeServer(probed)
            adopt(server: probed)
            if probed.config.authDisabled {
                // Nothing to sign in to: the server treats every request as
                // its one fixed user.
                state = .signedIn
            } else {
                state = await tokenStore.hasSession ? .signedIn : .signedOut
            }
        } catch {
            lastError = (error as? ServerProbeError)?.errorMessage ?? error.localizedDescription
            throw error
        }
    }

    /// Run the platform's sign-in strategy and store the resulting tokens.
    ///
    /// `strategy` overrides the one this session was built with, which is how
    /// the tvOS screen supplies a ``DeviceCodeAuthenticator`` bound to its own
    /// "show this code" callback without the session knowing about either.
    public func signIn(using strategy: (any Authenticating)? = nil) async throws {
        guard let server else { throw ServerProbeError.invalidURL }
        // A server with no auth is already as signed in as it gets. Answering
        // this rather than throwing keeps a stray "Sign in" button harmless.
        guard !server.config.authDisabled else {
            state = .signedIn
            return
        }
        guard let authenticator = strategy ?? self.authenticator else { throw OIDCError.invalidConfiguration }
        guard let issuer = server.config.issuerURL else { throw ServerProbeError.oidcNotConfigured }
        lastError = nil

        do {
            let configuration = try await OIDCClient.discover(issuer: issuer, session: session)
            let client = OIDCClient(
                configuration: configuration,
                clientID: server.config.oidcClientId,
                redirectURI: redirectURI,
                session: session
            )
            oidc = client
            await tokenStore.configure(client: client)

            let tokens = try await authenticator.authorize(using: client)
            try await tokenStore.adopt(tokens)
            self.state = .signedIn
        } catch {
            lastError = signInMessage(for: error)
            throw error
        }
    }

    /// Deliberate sign-out. Keeps the server so the next sign-in is one tap.
    ///
    /// On a server with no auth there is no session to drop, and leaving the
    /// state at `signedOut` would strand the app on a sign-in screen that
    /// cannot do anything — so the only honest sign-out there is to forget the
    /// server. The UI calls it "Disconnect" for exactly that reason.
    public func signOut() async {
        if server?.config.authDisabled == true {
            await forgetServer()
            return
        }
        await tokenStore.clear()
        state = server == nil ? .needsServer : .signedOut
    }

    /// Forget the server too — the "connect to a different server" path.
    public func forgetServer() async {
        await tokenStore.clear()
        defaults.removeObject(forKey: AuthSession.serverKey)
        server = nil
        client = nil
        oidc = nil
        state = .needsServer
    }

    // MARK: - Internals

    private func adopt(server: FlimmServer) {
        self.server = server
        guard !server.config.authDisabled else {
            // The value is ignored by a server running without auth, but it
            // must be *sent*: /media accepts a bearer header or the signed
            // cookie, and AVPlayer only ever carries the header.
            self.client = APIClient(
                baseURL: server.baseURL,
                tokens: StaticTokenProvider(AuthSession.authDisabledToken),
                session: session
            )
            return
        }
        self.client = APIClient(baseURL: server.baseURL, tokens: tokenStore, session: session)
        // A restored session must be able to refresh its access token without
        // signing in again, so the store learns how to build its OIDC client
        // lazily — discovery runs on the first refresh, not at launch, and a
        // failure there is a transient error rather than a sign-out.
        let issuer = server.config.issuerURL
        let clientID = server.config.oidcClientId
        let redirectURI = redirectURI
        let session = session
        Task { [tokenStore, weak self] in
            await tokenStore.configure(clientProvider: {
                guard let issuer else { throw ServerProbeError.oidcNotConfigured }
                let configuration = try await OIDCClient.discover(issuer: issuer, session: session)
                return OIDCClient(configuration: configuration, clientID: clientID, redirectURI: redirectURI, session: session)
            })
            await tokenStore.onSignOut { [weak self] in
                await self?.handleDefinitiveSignOut()
            }
        }
    }

    private func handleDefinitiveSignOut() {
        // Reached only from `invalid_grant`: the refresh token is dead, and no
        // amount of retrying brings it back.
        state = .signedOut
        lastError = OIDCError.invalidGrant.errorMessage
    }

    private func storedServer() -> FlimmServer? {
        guard let data = defaults.data(forKey: AuthSession.serverKey) else { return nil }
        return try? JSONDecoder().decode(FlimmServer.self, from: data)
    }

    private func storeServer(_ server: FlimmServer) {
        guard let data = try? JSONEncoder().encode(server) else { return }
        defaults.set(data, forKey: AuthSession.serverKey)
    }

    /// The redirect URI is a deployment-side setting, and "invalid redirect" is
    /// the single most common first-run failure — so the message names it.
    private func signInMessage(for error: any Error) -> String {
        guard let oidcError = error as? OIDCError else { return error.localizedDescription }
        switch oidcError {
        #if !os(tvOS)
        // Only the browser flow has a redirect URI to get wrong; the device
        // grant never leaves the provider's own page.
        case .authorizationFailed(let detail):
            return """
            \(detail)

            If the provider rejected the redirect, allow this exact native \
            redirect URI on the client: \(redirectURI.absoluteString)
            """
        #endif
        default:
            return oidcError.errorMessage
        }
    }
}
