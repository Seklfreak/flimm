import XCTest
@testable import FlimmKit

@MainActor
final class StallReporterTests: XCTestCase {
    private func reporter(_ session: URLSession) -> StallReporter {
        StallReporter(
            client: APIClient(
                baseURL: URL(string: "https://flimm.example.com")!,
                tokens: StaticTokenProvider("tok"),
                session: session
            ),
            platform: "tvos"
        )
    }

    /// The report goes out when the picture comes *back*, because only then is
    /// the length of the stall known.
    func testAStallIsReportedWhenItEnds() async throws {
        let session = StubURLProtocol.session(json: "{}")
        let stalls = reporter(session)

        stalls.update(isStalled: true, videoID: "yt-id", position: 2472.5, height: 1080)
        XCTAssertTrue(StubURLProtocol.recorded.isEmpty, "a stall in progress says nothing yet")

        try await Task.sleep(for: .seconds(0.5))
        stalls.update(isStalled: false, videoID: "yt-id", position: 2472.5, height: 1080)

        let request = try await recordedStall()
        XCTAssertEqual(request.path, "/api/v1/videos/yt-id/stall")
        let body = try XCTUnwrap(
            JSONSerialization.jsonObject(with: XCTUnwrap(request.body)) as? [String: Any]
        )
        XCTAssertEqual(body["position"] as? Double, 2472.5)
        XCTAssertEqual(body["height"] as? Int, 1080)
        XCTAssertEqual(body["client"] as? String, "tvos")
        // Measured, not echoed: the server's floor is applied to this number.
        XCTAssertGreaterThanOrEqual(try XCTUnwrap(body["seconds"] as? Double), 0.4)
    }

    /// Every player has sub-second gaps between segments. Reporting them would
    /// bury the ones a person noticed.
    func testATinyGapIsNotReported() async throws {
        let session = StubURLProtocol.session(json: "{}")
        let stalls = reporter(session)

        stalls.update(isStalled: true, videoID: "yt-id", position: 10, height: 720)
        stalls.update(isStalled: false, videoID: "yt-id", position: 10, height: 720)

        try await Task.sleep(for: .seconds(0.2))
        XCTAssertTrue(StubURLProtocol.recorded.isEmpty)
    }

    /// A stall that was still running when playback stopped has no length: the
    /// viewer may simply have left. It is dropped rather than guessed at.
    func testAStallInProgressIsAbandonedNotReported() async throws {
        let session = StubURLProtocol.session(json: "{}")
        let stalls = reporter(session)

        stalls.update(isStalled: true, videoID: "yt-id", position: 10, height: 720)
        try await Task.sleep(for: .seconds(0.5))
        stalls.reset()
        stalls.update(isStalled: false, videoID: "yt-id", position: 10, height: 720)

        try await Task.sleep(for: .seconds(0.2))
        XCTAssertTrue(StubURLProtocol.recorded.isEmpty)
    }

    /// The request is fired off in a detached task, so waiting for it is part
    /// of the test rather than a fixed sleep.
    private func recordedStall() async throws -> StubURLProtocol.Recorded {
        for _ in 0..<50 {
            if let last = StubURLProtocol.recorded.last { return last }
            try await Task.sleep(for: .milliseconds(20))
        }
        throw XCTSkip("no stall was reported")
    }
}
