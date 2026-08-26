import XCTest
@testable import FlimmKit

final class OIDCClientTests: XCTestCase {
    private let configuration = OIDCConfiguration(
        issuer: "https://auth.example.com/application/o/flimm/",
        authorizationEndpoint: URL(string: "https://auth.example.com/application/o/authorize/")!,
        tokenEndpoint: URL(string: "https://auth.example.com/application/o/token/")!
    )

    private func client(session: URLSession = .shared) -> OIDCClient {
        OIDCClient(
            configuration: configuration,
            clientID: "flimm-native",
            redirectURI: URL(string: "dev.winktech.flimm://auth")!,
            session: session
        )
    }

    func testAuthorizationURL() throws {
        let pkce = PKCE(verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
        let url = try client().authorizationURL(pkce: pkce, state: "st-1")
        let items = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems)
        let query = Dictionary(uniqueKeysWithValues: items.map { ($0.name, $0.value ?? "") })

        XCTAssertEqual(query["response_type"], "code")
        XCTAssertEqual(query["client_id"], "flimm-native")
        XCTAssertEqual(query["redirect_uri"], "dev.winktech.flimm://auth")
        // offline_access is what earns a refresh token; without it the app
        // silently logs out when the access token expires.
        XCTAssertEqual(query["scope"], "openid profile email offline_access")
        XCTAssertEqual(query["state"], "st-1")
        XCTAssertEqual(query["code_challenge"], "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
        XCTAssertEqual(query["code_challenge_method"], "S256")
    }

    func testCallbackParsing() throws {
        let subject = client()
        let callback = URL(string: "dev.winktech.flimm://auth?code=abc123&state=st-1")!
        XCTAssertEqual(try subject.code(from: callback, state: "st-1"), "abc123")

        // A replayed or crossed callback must not be accepted.
        XCTAssertThrowsError(try subject.code(from: callback, state: "other")) { error in
            XCTAssertEqual(error as? OIDCError, .stateMismatch)
        }

        let denied = URL(string: "dev.winktech.flimm://auth?error=access_denied&error_description=Nope&state=st-1")!
        XCTAssertThrowsError(try subject.code(from: denied, state: "st-1")) { error in
            XCTAssertEqual(error as? OIDCError, .authorizationFailed("Nope"))
        }
    }

    func testTokenExchangePostsAFormAndDecodesTheResponse() async throws {
        let session = StubURLProtocol.session { _, _ in
            (200, Data(#"{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":300}"#.utf8))
        }
        let tokens = try await client(session: session).exchange(
            code: "abc123",
            pkce: PKCE(verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
        )

        XCTAssertEqual(tokens.accessToken, "at")
        XCTAssertEqual(tokens.refreshToken, "rt")
        XCTAssertFalse(tokens.isExpired())

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.header("Content-Type"), "application/x-www-form-urlencoded")
        let body = String(data: try XCTUnwrap(request.body), encoding: .utf8) ?? ""
        XCTAssertTrue(body.contains("grant_type=authorization_code"))
        XCTAssertTrue(body.contains("code_verifier=dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"))
        XCTAssertTrue(body.contains("redirect_uri=dev.winktech.flimm%3A%2F%2Fauth"))
    }

    /// The one failure that ends a session.
    func testInvalidGrantIsDistinguished() async {
        let session = StubURLProtocol.session { _, _ in
            (400, Data(#"{"error":"invalid_grant","error_description":"Token is not active"}"#.utf8))
        }
        do {
            _ = try await client(session: session).refresh(refreshToken: "rt")
            XCTFail("expected invalidGrant")
        } catch {
            XCTAssertEqual(error as? OIDCError, .invalidGrant)
        }
    }

    /// A provider 500 must *not* look like a dead refresh token.
    func testServerErrorIsNotInvalidGrant() async {
        let session = StubURLProtocol.session { _, _ in (500, Data(#"{"error":"server_error"}"#.utf8)) }
        do {
            _ = try await client(session: session).refresh(refreshToken: "rt")
            XCTFail("expected a failure")
        } catch {
            XCTAssertNotEqual(error as? OIDCError, .invalidGrant)
        }
    }

    func testDiscovery() async throws {
        let session = StubURLProtocol.session { _, _ in
            (200, Data("""
            {
              "issuer": "https://auth.example.com/application/o/flimm/",
              "authorization_endpoint": "https://auth.example.com/application/o/authorize/",
              "token_endpoint": "https://auth.example.com/application/o/token/",
              "end_session_endpoint": "https://auth.example.com/application/o/flimm/end-session/"
            }
            """.utf8))
        }
        let configuration = try await OIDCClient.discover(
            issuer: URL(string: "https://auth.example.com/application/o/flimm/")!,
            session: session
        )
        XCTAssertEqual(configuration.tokenEndpoint.absoluteString, "https://auth.example.com/application/o/token/")
        XCTAssertNotNil(configuration.endSessionEndpoint)

        // The trailing slash on the issuer must not produce a double slash.
        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/application/o/flimm/.well-known/openid-configuration")
    }

    func testExpiryLeeway() {
        let almostExpired = OIDCTokens(
            accessToken: "at", refreshToken: "rt", idToken: nil,
            expiresAt: Date().addingTimeInterval(30)
        )
        // A token that dies in 30s must not be sent — it would expire in flight.
        XCTAssertTrue(almostExpired.isExpired())

        let fresh = OIDCTokens(accessToken: "at", refreshToken: "rt", idToken: nil, expiresAt: Date().addingTimeInterval(600))
        XCTAssertFalse(fresh.isExpired())
    }
}
