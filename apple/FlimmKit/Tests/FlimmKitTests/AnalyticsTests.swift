import XCTest
@testable import FlimmKit

@MainActor
final class AnalyticsTests: XCTestCase {
    private let endpoint = URL(string: "https://stats.example.com")!

    override func tearDown() {
        Analytics.reset()
        super.tearDown()
    }

    /// Waits for the queue to have posted `count` events. Delivery is one
    /// request at a time with a completion hop back to the main actor, so
    /// polling is what "the queue drained" looks like from a test.
    private func waitForRequests(_ count: Int, timeout: TimeInterval = 2) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while StubURLProtocol.recorded.count < count, Date() < deadline {
            try await Task.sleep(nanoseconds: 5_000_000)
        }
        XCTAssertEqual(StubURLProtocol.recorded.count, count)
    }

    private func payload(_ index: Int) throws -> [String: Any] {
        let recorded = StubURLProtocol.recorded[index]
        let body = try XCTUnwrap(recorded.body)
        let json = try XCTUnwrap(try JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(json["type"] as? String, "event")
        return try XCTUnwrap(json["payload"] as? [String: Any])
    }

    func testScreenPostsThePatternNotTheId() async throws {
        let session = StubURLProtocol.session(json: "{}")
        Analytics.configure(endpoint: endpoint, websiteID: "site-1", session: session)
        Analytics.screen(.watch)
        try await waitForRequests(1)

        let recorded = StubURLProtocol.recorded[0]
        XCTAssertEqual(recorded.url?.path, "/api/send")
        XCTAssertEqual(recorded.method, "POST")
        // Umami answers a request with no User-Agent, or one its bot filter
        // matches, with a 200 that stores nothing.
        XCTAssertTrue(recorded.header("User-Agent")?.hasPrefix("Mozilla/5.0") ?? false)

        let payload = try payload(0)
        XCTAssertEqual(payload["website"] as? String, "site-1")
        XCTAssertEqual(payload["url"] as? String, "/watch/:id")
        XCTAssertEqual(payload["title"] as? String, "Watch")
        XCTAssertFalse((payload["id"] as? String ?? "").isEmpty)
        // A payload with no name is what Umami stores as a pageview.
        XCTAssertNil(payload["name"])
    }

    func testEventIsFiledAgainstTheScreenItHappenedOn() async throws {
        let session = StubURLProtocol.session(json: "{}")
        Analytics.configure(endpoint: endpoint, websiteID: "site-1", session: session)
        Analytics.screen(.watch)
        Analytics.play(videoID: "v1", kind: "video", audioOnly: true)
        try await waitForRequests(2)

        let event = try payload(1)
        XCTAssertEqual(event["name"] as? String, "play")
        XCTAssertEqual(event["url"] as? String, "/watch/:id")
        XCTAssertEqual(event["data"] as? [String: String], ["kind": "video", "audio": "yes"])
    }

    func testSearchReportsTheScopeAndNeverTheQuery() async throws {
        let session = StubURLProtocol.session(json: "{}")
        Analytics.configure(endpoint: endpoint, websiteID: "site-1", session: session)
        Analytics.screen(.search)
        Analytics.search(scope: "subtitles")
        try await waitForRequests(2)

        let event = try payload(1)
        XCTAssertEqual(event["data"] as? [String: String], ["scope": "subtitles"])
    }

    /// A pushed view popping back re-fires `onAppear` on the root; that bounce
    /// is not a second visit.
    func testTheSameScreenTwiceInARowIsReportedOnce() async throws {
        let session = StubURLProtocol.session(json: "{}")
        Analytics.configure(endpoint: endpoint, websiteID: "site-1", session: session)
        Analytics.screen(.history)
        Analytics.screen(.history)
        try await waitForRequests(1)
    }

    func testNothingIsSentWithoutConfiguration() async throws {
        _ = StubURLProtocol.session(json: "{}")
        Analytics.screen(.home)
        Analytics.play(videoID: "v1", kind: "video", audioOnly: false)
        try await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(StubURLProtocol.recorded.count, 0)
    }

    /// The deployment's opt-out wins over whatever the app was built with, and
    /// takes the backlog with it.
    func testAServerThatDisablesAnalyticsStopsReporting() async throws {
        let session = StubURLProtocol.session { _, _ in
            throw URLError(.notConnectedToInternet)
        }
        Analytics.configure(endpoint: endpoint, websiteID: "site-1", session: session)
        Analytics.screen(.home)
        try await waitForRequests(1)

        Analytics.apply(ServerConfig(analyticsDisabled: true))
        Analytics.screen(.settings)
        Analytics.feedCreated()
        try await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(StubURLProtocol.recorded.count, 1, "reported to a server that opted out")
    }

    func testAServerThatSaysNothingLeavesAnalyticsOn() async throws {
        let session = StubURLProtocol.session(json: "{}")
        Analytics.configure(endpoint: endpoint, websiteID: "site-1", session: session)
        Analytics.apply(ServerConfig())
        Analytics.screen(.channels)
        try await waitForRequests(1)
    }

    func testTheServerConfigDecodesTheOptOut() throws {
        let json = Data(#"{"app_name":"Flimm","analytics_disabled":true}"#.utf8)
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        let config = try decoder.decode(ServerConfig.self, from: json)
        XCTAssertTrue(config.analyticsDisabled)
    }
}
