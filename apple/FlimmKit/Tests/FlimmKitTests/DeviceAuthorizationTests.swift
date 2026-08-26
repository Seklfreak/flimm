import XCTest
@testable import FlimmKit

/// RFC 8628, the tvOS sign-in path. The polling rules are the whole risk here:
/// a client that treats `authorization_pending` or `slow_down` as a failure
/// never signs anyone in.
final class DeviceAuthorizationTests: XCTestCase {
    private let deviceEndpoint = URL(string: "https://auth.example.com/application/o/device/")!

    private func configuration(device: Bool) -> OIDCConfiguration {
        OIDCConfiguration(
            issuer: "https://auth.example.com/application/o/flimm/",
            authorizationEndpoint: URL(string: "https://auth.example.com/application/o/authorize/")!,
            tokenEndpoint: URL(string: "https://auth.example.com/application/o/token/")!,
            deviceAuthorizationEndpoint: device ? deviceEndpoint : nil
        )
    }

    private func client(session: URLSession, device: Bool = true) -> OIDCClient {
        OIDCClient(
            configuration: configuration(device: device),
            clientID: "flimm-native",
            redirectURI: URL(string: "dev.winktech.flimm://auth")!,
            session: session
        )
    }

    // MARK: - Discovery

    func testDiscoveryPicksUpTheDeviceEndpoint() async throws {
        let session = StubURLProtocol.session { _, _ in
            (200, Data("""
            {
              "issuer": "https://auth.example.com/application/o/flimm/",
              "authorization_endpoint": "https://auth.example.com/application/o/authorize/",
              "token_endpoint": "https://auth.example.com/application/o/token/",
              "device_authorization_endpoint": "https://auth.example.com/application/o/device/"
            }
            """.utf8))
        }
        let configuration = try await OIDCClient.discover(
            issuer: URL(string: "https://auth.example.com/application/o/flimm/")!,
            session: session
        )
        XCTAssertEqual(configuration.deviceAuthorizationEndpoint, deviceEndpoint)
    }

    /// A provider without the grant is a hard stop on Apple TV: there is no
    /// browser to fall back to, so the app has to say exactly that.
    func testMissingEndpointIsUnsupportedRatherThanAGenericFailure() async {
        let session = StubURLProtocol.session(json: "{}")
        do {
            _ = try await client(session: session, device: false).deviceAuthorize()
            XCTFail("expected deviceFlowUnsupported")
        } catch {
            XCTAssertEqual(error as? OIDCError, .deviceFlowUnsupported)
        }
    }

    // MARK: - Authorization request

    func testDeviceAuthorizePostsScopesAndDecodesTheResponse() async throws {
        let session = StubURLProtocol.session { _, _ in
            (200, Data("""
            {
              "device_code": "dc-1",
              "user_code": "WDJBMJHT",
              "verification_uri": "https://auth.example.com/device",
              "verification_uri_complete": "https://auth.example.com/device?code=WDJBMJHT",
              "expires_in": 600,
              "interval": 7
            }
            """.utf8))
        }
        let authorization = try await client(session: session).deviceAuthorize()

        XCTAssertEqual(authorization.deviceCode, "dc-1")
        XCTAssertEqual(authorization.userCode, "WDJBMJHT")
        // Grouped for someone reading it across a living room.
        XCTAssertEqual(authorization.displayCode, "WDJB-MJHT")
        XCTAssertEqual(authorization.scannableURI.absoluteString, "https://auth.example.com/device?code=WDJBMJHT")
        XCTAssertEqual(authorization.interval, 7)
        XCTAssertGreaterThan(authorization.expiresAt.timeIntervalSinceNow, 500)

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.path, "/application/o/device")
        let body = String(data: try XCTUnwrap(request.body), encoding: .utf8) ?? ""
        XCTAssertTrue(body.contains("client_id=flimm-native"))
        // offline_access is what earns a refresh token on this path too.
        XCTAssertTrue(body.contains("scope=openid%20profile%20email%20offline_access"))
    }

    /// A provider that advertises nothing usable, or rejects this client id,
    /// is the same problem from the user's side.
    func testRejectedClientReadsAsUnsupported() async {
        let session = StubURLProtocol.session { _, _ in (400, Data(#"{"error":"invalid_client"}"#.utf8)) }
        do {
            _ = try await client(session: session).deviceAuthorize()
            XCTFail("expected deviceFlowUnsupported")
        } catch {
            XCTAssertEqual(error as? OIDCError, .deviceFlowUnsupported)
        }
    }

    func testMissingVerificationURIIsRejected() async {
        let session = StubURLProtocol.session(json: #"{"device_code":"dc","user_code":"AB","verification_uri":""}"#)
        do {
            _ = try await client(session: session).deviceAuthorize()
            XCTFail("expected a failure")
        } catch {
            XCTAssertEqual(error as? OIDCError, .invalidConfiguration)
        }
    }

    // MARK: - Polling rules

    /// `slow_down` means "wait longer", not "give up".
    func testSlowDownExtendsTheIntervalAndPendingDoesNot() {
        XCTAssertEqual(
            DeviceFlow.nextInterval(after: "slow_down", current: .seconds(5), increment: .seconds(5)),
            .seconds(10)
        )
        XCTAssertEqual(
            DeviceFlow.nextInterval(after: "authorization_pending", current: .seconds(5), increment: .seconds(5)),
            .seconds(5)
        )
    }

    func testOnlyTerminalErrorsEndTheFlow() {
        XCTAssertNil(DeviceFlow.failure(for: "authorization_pending", description: nil))
        XCTAssertNil(DeviceFlow.failure(for: "slow_down", description: nil))
        XCTAssertEqual(DeviceFlow.failure(for: "expired_token", description: nil), .deviceCodeExpired)
        XCTAssertEqual(
            DeviceFlow.failure(for: "access_denied", description: nil),
            .authorizationFailed("Sign-in was declined.")
        )
        XCTAssertEqual(
            DeviceFlow.failure(for: "server_error", description: "Boom"),
            .tokenExchangeFailed("Boom")
        )
    }

    // MARK: - Polling

    func testPollingKeepsGoingThroughPendingAndSlowDown() async throws {
        let counter = PollCounter()
        let session = StubURLProtocol.session { _, _ in
            switch counter.next() {
            case 1: (400, Data(#"{"error":"authorization_pending"}"#.utf8))
            case 2: (400, Data(#"{"error":"slow_down"}"#.utf8))
            default: (200, Data(#"{"access_token":"at","refresh_token":"rt","expires_in":300}"#.utf8))
            }
        }
        let tokens = try await client(session: session).pollForDeviceToken(
            pending(interval: 0),
            slowDownIncrement: .zero
        )
        XCTAssertEqual(tokens.accessToken, "at")
        XCTAssertEqual(tokens.refreshToken, "rt")
        XCTAssertEqual(counter.value, 3)
    }

    func testPollingStopsWhenTheCodeExpires() async {
        let session = StubURLProtocol.session { _, _ in (400, Data(#"{"error":"expired_token"}"#.utf8)) }
        do {
            _ = try await client(session: session).pollForDeviceToken(pending(interval: 0), slowDownIncrement: .zero)
            XCTFail("expected deviceCodeExpired")
        } catch {
            XCTAssertEqual(error as? OIDCError, .deviceCodeExpired)
        }
    }

    /// The deadline is the client's too: a provider that never answers must
    /// not leave the TV polling forever.
    func testPollingStopsAtTheLocalDeadline() async {
        let session = StubURLProtocol.session { _, _ in (400, Data(#"{"error":"authorization_pending"}"#.utf8)) }
        let expired = DeviceAuthorization(
            deviceCode: "dc-1",
            userCode: "WDJBMJHT",
            verificationURI: URL(string: "https://auth.example.com/device")!,
            expiresAt: Date().addingTimeInterval(-1),
            interval: 0
        )
        do {
            _ = try await client(session: session).pollForDeviceToken(expired, slowDownIncrement: .zero)
            XCTFail("expected deviceCodeExpired")
        } catch {
            XCTAssertEqual(error as? OIDCError, .deviceCodeExpired)
        }
    }

    func testPollSendsTheDeviceGrant() async throws {
        let session = StubURLProtocol.session { _, _ in
            (200, Data(#"{"access_token":"at","expires_in":300}"#.utf8))
        }
        _ = try await client(session: session).pollForDeviceToken(pending(interval: 0), slowDownIncrement: .zero)

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/application/o/token")
        let body = String(data: try XCTUnwrap(request.body), encoding: .utf8) ?? ""
        XCTAssertTrue(body.contains("grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code"))
        XCTAssertTrue(body.contains("device_code=dc-1"))
    }

    // MARK: - Strategy

    func testDeviceAuthenticatorShowsTheCodeBeforeItPolls() async throws {
        let counter = PollCounter()
        let session = StubURLProtocol.session { request, _ in
            if request.url?.path == "/application/o/device" {
                return (200, Data("""
                {"device_code":"dc-1","user_code":"WDJBMJHT",
                 "verification_uri":"https://auth.example.com/device","expires_in":600,"interval":0}
                """.utf8))
            }
            return counter.next() == 1
                ? (400, Data(#"{"error":"authorization_pending"}"#.utf8))
                : (200, Data(#"{"access_token":"at","expires_in":300}"#.utf8))
        }
        let shown = Box<DeviceAuthorization>()
        let authenticator = DeviceCodeAuthenticator(slowDownIncrement: .zero) { authorization in
            shown.value = authorization
        }
        let tokens = try await authenticator.authorize(using: client(session: session))

        XCTAssertEqual(tokens.accessToken, "at")
        // The screen must have the code while polling is still running —
        // otherwise nobody can approve it.
        XCTAssertEqual(shown.value?.userCode, "WDJBMJHT")
    }

    // MARK: - Helpers

    private func pending(interval: Int) -> DeviceAuthorization {
        DeviceAuthorization(
            deviceCode: "dc-1",
            userCode: "WDJBMJHT",
            verificationURI: URL(string: "https://auth.example.com/device")!,
            expiresAt: Date().addingTimeInterval(600),
            interval: interval
        )
    }
}

/// Counts stub responses across the concurrency boundary the stub sits on.
private final class PollCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0

    func next() -> Int {
        lock.withLock {
            count += 1
            return count
        }
    }

    var value: Int { lock.withLock { count } }
}

private final class Box<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: Value?

    var value: Value? {
        get { lock.withLock { stored } }
        set { lock.withLock { stored = newValue } }
    }
}
