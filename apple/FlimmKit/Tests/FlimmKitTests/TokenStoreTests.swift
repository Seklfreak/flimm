import XCTest
@testable import FlimmKit

final class TokenStoreTests: XCTestCase {
    private func tokens(expiresIn: TimeInterval, refresh: String? = "rt") -> OIDCTokens {
        OIDCTokens(accessToken: "at", refreshToken: refresh, idToken: nil, expiresAt: Date().addingTimeInterval(expiresIn))
    }

    private func client(session: URLSession) -> OIDCClient {
        OIDCClient(
            configuration: OIDCConfiguration(
                issuer: "https://auth.example.com",
                authorizationEndpoint: URL(string: "https://auth.example.com/authorize")!,
                tokenEndpoint: URL(string: "https://auth.example.com/token")!
            ),
            clientID: "flimm-native",
            redirectURI: URL(string: "dev.winktech.flimm://auth")!,
            session: session
        )
    }

    func testPersistsAndReloads() async throws {
        let secrets = InMemorySecretStore()
        let store = TokenStore(store: secrets)
        try await store.adopt(tokens(expiresIn: 600))

        let reloaded = TokenStore(store: secrets)
        let loaded = await reloaded.load()
        XCTAssertEqual(loaded?.accessToken, "at")
        let token = try await reloaded.accessToken()
        XCTAssertEqual(token, "at")
    }

    func testExpiredTokenIsRefreshedBeforeUse() async throws {
        let session = StubURLProtocol.session { _, _ in
            (200, Data(#"{"access_token":"at-2","token_type":"Bearer","expires_in":600}"#.utf8))
        }
        let store = TokenStore(store: InMemorySecretStore())
        await store.configure(client: client(session: session))
        try await store.adopt(tokens(expiresIn: 5))

        let token = try await store.accessToken()
        XCTAssertEqual(token, "at-2")

        // The provider did not rotate the refresh token, so the old one is
        // kept rather than lost.
        let current = await store.current
        XCTAssertEqual(current?.refreshToken, "rt")
    }

    /// A transient failure must leave the session intact — this is the rule
    /// that keeps a user on a flaky network from being logged out.
    func testTransientRefreshFailureKeepsTheSession() async throws {
        let session = StubURLProtocol.session { _, _ in (503, Data(#"{"error":"temporarily_unavailable"}"#.utf8)) }
        let store = TokenStore(store: InMemorySecretStore())
        await store.configure(client: client(session: session))
        try await store.adopt(tokens(expiresIn: 5))

        do {
            _ = try await store.refreshAccessToken()
            XCTFail("expected a failure")
        } catch {
            XCTAssertNotEqual(error as? OIDCError, .invalidGrant)
        }
        let stillThere = await store.hasSession
        XCTAssertTrue(stillThere)
    }

    func testInvalidGrantClearsTheSessionAndNotifiesOnce() async throws {
        let session = StubURLProtocol.session { _, _ in (400, Data(#"{"error":"invalid_grant"}"#.utf8)) }
        let secrets = InMemorySecretStore()
        let store = TokenStore(store: secrets)
        await store.configure(client: client(session: session))
        try await store.adopt(tokens(expiresIn: 5))

        let counter = Counter()
        await store.onSignOut { await counter.increment() }

        do {
            _ = try await store.refreshAccessToken()
            XCTFail("expected invalidGrant")
        } catch {
            XCTAssertEqual(error as? OIDCError, .invalidGrant)
        }

        let hasSession = await store.hasSession
        XCTAssertFalse(hasSession)
        XCTAssertNil(try secrets.read("oidc-tokens"))
        let calls = await counter.value
        XCTAssertEqual(calls, 1)
    }

    func testNoRefreshTokenMeansNoRenewal() async throws {
        let store = TokenStore(store: InMemorySecretStore())
        try await store.adopt(tokens(expiresIn: 5, refresh: nil))
        let token = try await store.accessToken()
        XCTAssertNil(token)
    }
}

actor Counter {
    private(set) var value = 0
    func increment() { value += 1 }
}

extension TokenStoreTests {
    /// The bug that logged users out every morning: a session restored from
    /// the Keychain had tokens but no OIDC client, so the first refresh after
    /// a relaunch silently returned nil and the 401 became "Signed out".
    func testRestoredSessionRefreshesThroughTheLazyProvider() async throws {
        let session = StubURLProtocol.session { _, _ in
            (200, Data(#"{"access_token":"at-2","refresh_token":"rt-2","token_type":"Bearer","expires_in":600}"#.utf8))
        }
        let secrets = InMemorySecretStore()
        let signedIn = TokenStore(store: secrets)
        try await signedIn.adopt(tokens(expiresIn: 5))

        // A fresh process: load from the store, no client handed over.
        let restored = TokenStore(store: secrets)
        _ = await restored.load()
        let built = SendableCounter()
        await restored.configure(clientProvider: { [client = self.client(session: session)] in
            await built.increment()
            return client
        })

        let first = try await restored.accessToken()
        XCTAssertEqual(first, "at-2")
        _ = try await restored.refreshAccessToken()
        let count = await built.value
        XCTAssertEqual(count, 1, "the provider builds the client once and caches it")
        let rotated = await restored.current?.refreshToken
        XCTAssertEqual(rotated, "rt-2", "the rotated token was adopted")
    }

    /// Discovery failing (VPN down) must read as transient, not as a sign-out.
    func testProviderFailureIsTransient() async throws {
        let store = TokenStore(store: InMemorySecretStore())
        try await store.adopt(tokens(expiresIn: 5))
        await store.configure(clientProvider: { throw ServerProbeError.unreachable("vpn down") })
        do {
            _ = try await store.accessToken()
            XCTFail("expected the provider's error")
        } catch let error as ServerProbeError {
            XCTAssertEqual(error, .unreachable("vpn down"))
        }
        let remaining = await store.current
        XCTAssertNotNil(remaining, "tokens survive a failed refresh")
    }
}

private actor SendableCounter {
    private(set) var value = 0
    func increment() { value += 1 }
}
