import XCTest
@testable import FlimmKit

/// The three failures need visibly different messages: bad typing, wrong host,
/// and a deployment that must be reconfigured. A friendly failure here is most
/// of the setup UX.
final class ServerProbeTests: XCTestCase {
    func testProbeSucceeds() async throws {
        let session = StubURLProtocol.session(json: Fixtures.serverConfig)
        let server = try await ServerProbe(session: session).probe("flimm.example.com")
        XCTAssertEqual(server.baseURL.absoluteString, "https://flimm.example.com")
        XCTAssertEqual(server.config.oidcClientId, "flimm-native")

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/api/v1/config")
    }

    func testInvalidURL() async {
        let probe = ServerProbe(session: StubURLProtocol.session(json: Fixtures.serverConfig))
        await XCTAssertThrows(ServerProbeError.invalidURL) { try await probe.probe("   ") }
    }

    /// Offline, or a deployment deliberately kept off the public internet.
    /// Retrying later is reasonable, so it must not read as "wrong address".
    func testUnreachable() async {
        let session = StubURLProtocol.session { _, _ in throw URLError(.cannotFindHost) }
        let probe = ServerProbe(session: session)
        do {
            _ = try await probe.probe("flimm.example.com")
            XCTFail("expected unreachable")
        } catch let error as ServerProbeError {
            guard case .unreachable = error else { return XCTFail("got \(error)") }
        } catch {
            XCTFail("got \(error)")
        }
    }

    func testSomethingElseAnswering() async {
        let session = StubURLProtocol.session { _, _ in (200, Data("<html>hello</html>".utf8)) }
        let probe = ServerProbe(session: session)
        await XCTAssertThrows(ServerProbeError.notAFlimmServer) { try await probe.probe("example.com") }
    }

    func testNotFoundIsNotAFlimmServer() async {
        let session = StubURLProtocol.session { _, _ in (404, Data(#"{"error":"not found"}"#.utf8)) }
        let probe = ServerProbe(session: session)
        await XCTAssertThrows(ServerProbeError.notAFlimmServer) { try await probe.probe("example.com") }
    }

    /// A server that says it runs without authentication is one to connect
    /// to, not one to refuse: the operator turned auth off deliberately, the
    /// web client already honours it, and there is no credential to protect.
    func testAuthDisabledServerIsAccepted() async throws {
        let session = StubURLProtocol.session(json: Fixtures.serverConfigAuthDisabled)
        let server = try await ServerProbe(session: session).probe("localhost:8080")
        XCTAssertTrue(server.config.authDisabled)
        XCTAssertFalse(server.config.hasOIDC)
        XCTAssertTrue(server.config.isUsable)
    }

    /// A server that wants authentication but publishes no issuer is a
    /// different thing entirely — half-configured, and still refused.
    func testOIDCNotConfigured() async {
        let session = StubURLProtocol.session(json: Fixtures.serverConfigWithoutOIDC)
        let probe = ServerProbe(session: session)
        await XCTAssertThrows(ServerProbeError.oidcNotConfigured) { try await probe.probe("flimm.example.com") }
    }

    func testNormalisationAcceptsPastedURLs() {
        XCTAssertEqual(ServerProbe.normalize(" flimm.example.com ")?.absoluteString, "https://flimm.example.com")
        XCTAssertEqual(ServerProbe.normalize("https://flimm.example.com/api/v1/")?.absoluteString, "https://flimm.example.com")
        XCTAssertEqual(ServerProbe.normalize("http://192.0.2.10:8080")?.absoluteString, "http://192.0.2.10:8080")
    }
}

func XCTAssertThrows(
    _ expected: ServerProbeError,
    file: StaticString = #filePath,
    line: UInt = #line,
    _ body: () async throws -> Void
) async {
    do {
        try await body()
        XCTFail("expected \(expected)", file: file, line: line)
    } catch let error as ServerProbeError {
        XCTAssertEqual(error, expected, file: file, line: line)
    } catch {
        XCTFail("got \(error)", file: file, line: line)
    }
}
