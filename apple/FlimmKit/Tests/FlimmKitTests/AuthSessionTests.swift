import XCTest
@testable import FlimmKit

/// A server running `AUTH_DISABLED=true` has no sign-in to run. The app must
/// come up usable against it — that is the whole point of the mode — while a
/// server that wants authentication and is merely misconfigured stays refused
/// (see ``ServerProbeTests``).
@MainActor
final class AuthSessionTests: XCTestCase {
    private let redirect = URL(string: "dev.winktech.flimm://auth")!

    private func session(_ urlSession: URLSession, defaults: UserDefaults) -> AuthSession {
        AuthSession(
            redirectURI: redirect,
            secrets: InMemorySecretStore(),
            defaults: defaults,
            session: urlSession,
            authenticator: nil
        )
    }

    private func emptyDefaults(_ name: String = #function) -> UserDefaults {
        let defaults = UserDefaults(suiteName: "flimm.tests.\(name)")!
        defaults.removePersistentDomain(forName: "flimm.tests.\(name)")
        return defaults
    }

    func testConnectingToAnAuthDisabledServerSignsStraightIn() async throws {
        let urlSession = StubURLProtocol.session(json: Fixtures.serverConfigAuthDisabled)
        let auth = session(urlSession, defaults: emptyDefaults())

        try await auth.connect(to: "localhost:8080")

        XCTAssertEqual(auth.state, .signedIn)
        XCTAssertFalse(auth.requiresSignIn)
    }

    /// `/media` accepts a bearer header or the signed cookie, and `AVPlayer`
    /// only ever carries the header — so requests must still carry one, even
    /// though the server ignores its value.
    func testRequestsStillCarryABearerHeader() async throws {
        let urlSession = StubURLProtocol.session { request, _ in
            let json = request.url?.path == "/api/v1/config"
                ? Fixtures.serverConfigAuthDisabled
                : Fixtures.me
            return (200, Data(json.utf8))
        }
        let auth = session(urlSession, defaults: emptyDefaults())
        try await auth.connect(to: "localhost:8080")

        _ = try await XCTUnwrap(auth.client).me()

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/api/v1/me")
        XCTAssertEqual(request.header("Authorization"), "Bearer \(AuthSession.authDisabledToken)")
    }

    /// A restored session must not land on a sign-in screen it can never get
    /// past: there is no token to find, and none is needed.
    func testRestoringAnAuthDisabledServerComesUpSignedIn() async throws {
        let defaults = emptyDefaults()
        let urlSession = StubURLProtocol.session(json: Fixtures.serverConfigAuthDisabled)
        try await session(urlSession, defaults: defaults).connect(to: "localhost:8080")

        let restored = session(urlSession, defaults: defaults)
        await restored.restore()

        XCTAssertEqual(restored.state, .signedIn)
    }

    /// The stored config is a snapshot; a server that gained a feature since
    /// (here: an APNs key) has to reach a phone that connected before it did,
    /// or the switch for it stays hidden for ever.
    func testRestoreLearnsWhatTheServerGainedSince() async throws {
        let defaults = emptyDefaults()
        try await session(StubURLProtocol.session(json: Fixtures.serverConfigAuthDisabled), defaults: defaults)
            .connect(to: "localhost:8080")

        let restored = session(StubURLProtocol.session(json: Fixtures.serverConfigWithPush), defaults: defaults)
        await restored.restore()
        XCTAssertEqual(restored.state, .signedIn)
        await restored.refreshConfig()
        XCTAssertTrue(restored.server?.config.pushEnabled ?? false)

        // And it is remembered: the next launch knows before asking.
        let again = session(StubURLProtocol.session { _, _ in (503, Data()) }, defaults: defaults)
        await again.restore()
        XCTAssertTrue(again.server?.config.pushEnabled ?? false)
        await again.refreshConfig()
        XCTAssertTrue(again.server?.config.pushEnabled ?? false, "an unreachable server must not take back what it said")
    }

    /// With nothing to sign out of, "sign out" can only mean leaving the
    /// server — anything else strands the app on a dead sign-in screen.
    func testSigningOutOfAnAuthDisabledServerForgetsIt() async throws {
        let urlSession = StubURLProtocol.session(json: Fixtures.serverConfigAuthDisabled)
        let auth = session(urlSession, defaults: emptyDefaults())
        try await auth.connect(to: "localhost:8080")

        await auth.signOut()

        XCTAssertEqual(auth.state, .needsServer)
        XCTAssertNil(auth.server)
    }

    /// A server with a provider is untouched by any of this.
    func testAServerWithOIDCStillNeedsASignIn() async throws {
        let urlSession = StubURLProtocol.session(json: Fixtures.serverConfig)
        let auth = session(urlSession, defaults: emptyDefaults())

        try await auth.connect(to: "flimm.example.com")

        XCTAssertEqual(auth.state, .signedOut)
        XCTAssertTrue(auth.requiresSignIn)
    }
}
