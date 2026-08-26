import XCTest
@testable import FlimmKit

final class ProgressReporterTests: XCTestCase {
    private let baseURL = URL(string: "https://flimm.example.com")!

    private func client(_ session: URLSession) -> APIClient {
        APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)
    }

    /// Pause, seek, background and terminate all funnel into `flush()`, and it
    /// must post whatever the player's position is right now.
    func testFlushPostsTheCurrentPosition() async throws {
        let session = StubURLProtocol.session(json: Fixtures.progressResult)
        let reporter = ProgressReporter(client: client(session), interval: .seconds(600))

        await reporter.start(videoId: "yt-id", context: .playlist("PL-music")) { 42 }
        await reporter.flush()

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/api/v1/videos/yt-id/progress")
        // The music-playlist context has to reach the server, or it records a
        // watch event it should be suppressing.
        XCTAssertEqual(request.query, "playlist=PL-music")
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(request.body)) as? [String: Any])
        XCTAssertEqual(body["position"] as? Int, 42)
    }

    func testRepeatedFlushAtTheSamePositionPostsOnce() async throws {
        let session = StubURLProtocol.session(json: Fixtures.progressResult)
        let reporter = ProgressReporter(client: client(session), interval: .seconds(600))

        await reporter.start(videoId: "yt-id") { 42 }
        await reporter.flush()
        await reporter.flush()
        await reporter.flush()

        XCTAssertEqual(StubURLProtocol.recorded.count, 1)
    }

    func testHeartbeatTicks() async throws {
        let session = StubURLProtocol.session(json: Fixtures.progressResult)
        let reporter = ProgressReporter(client: client(session), interval: .milliseconds(20))
        let position = MovingPosition()

        await reporter.start(videoId: "yt-id") { await position.next() }
        try await Task.sleep(for: .milliseconds(200))
        await reporter.stop()

        XCTAssertGreaterThanOrEqual(StubURLProtocol.recorded.count, 3)
    }

    /// Stepping to the next video must not lose the last position of the one
    /// being left.
    func testStartingANewVideoFlushesThePreviousOne() async throws {
        let session = StubURLProtocol.session(json: Fixtures.progressResult)
        let reporter = ProgressReporter(client: client(session), interval: .seconds(600))

        await reporter.start(videoId: "yt-1") { 10 }
        await reporter.start(videoId: "yt-2") { 20 }
        await reporter.stop()

        let paths = StubURLProtocol.recorded.compactMap(\.path)
        XCTAssertEqual(paths, ["/api/v1/videos/yt-1/progress", "/api/v1/videos/yt-2/progress"])
    }

    /// A dropped heartbeat is not worth surfacing, and must not stop the next
    /// one from going out.
    func testAFailedHeartbeatIsSwallowedAndRetriedAtTheSamePosition() async throws {
        let session = StubURLProtocol.session { _, _ in (503, Data(#"{"error":"nope"}"#.utf8)) }
        let reporter = ProgressReporter(client: client(session), interval: .seconds(600))

        await reporter.start(videoId: "yt-id") { 42 }
        await reporter.flush()
        // The first attempt failed, so the same position is sent again rather
        // than being suppressed as a duplicate.
        await reporter.flush()

        XCTAssertEqual(StubURLProtocol.recorded.count, 2)
    }
}

actor MovingPosition {
    private var seconds: Double = 0

    func next() -> Double {
        seconds += 10
        return seconds
    }
}
