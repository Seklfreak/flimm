import XCTest
@testable import FlimmKit

final class RemoteClockTests: XCTestCase {
    private let start = Date(timeIntervalSince1970: 1_000_000)

    private func session(position: Double, paused: Bool, speed: Double = 1, duration: Double = 600) -> RemoteSession {
        RemoteSession(
            id: "s1", videoId: "v1", position: position,
            duration: duration, paused: paused, speed: speed
        )
    }

    /// The reason this exists: a scrubber drawn from the published number alone
    /// would sit still between heartbeats.
    func testPlayingAdvancesWithTheWallClock() {
        let projected = RemoteClock.position(
            of: session(position: 100, paused: false),
            receivedAt: start,
            now: start.addingTimeInterval(7)
        )
        XCTAssertEqual(projected, 107, accuracy: 0.001)
    }

    func testSpeedIsHonoured() {
        let projected = RemoteClock.position(
            of: session(position: 100, paused: false, speed: 2),
            receivedAt: start,
            now: start.addingTimeInterval(10)
        )
        XCTAssertEqual(projected, 120, accuracy: 0.001)
    }

    func testPausedStandsStill() {
        let projected = RemoteClock.position(
            of: session(position: 100, paused: true),
            receivedAt: start,
            now: start.addingTimeInterval(300)
        )
        XCTAssertEqual(projected, 100, accuracy: 0.001)
    }

    /// A controller that ran past the end would show a video finishing over and
    /// over while it waited to hear that the next one had started.
    func testNeverRunsPastTheDuration() {
        let projected = RemoteClock.position(
            of: session(position: 590, paused: false, duration: 600),
            receivedAt: start,
            now: start.addingTimeInterval(120)
        )
        XCTAssertEqual(projected, 600, accuracy: 0.001)
    }

    /// A server that says nothing about speed is playing at 1×. A zero would
    /// stop the controller's clock dead.
    func testMissingSpeedDecodesAsNormalPlayback() throws {
        let json = #"{"id":"s1","video_id":"v1","position":10,"duration":600}"#
        let decoded = try FlimmCoding.decoder.decode(RemoteSession.self, from: Data(json.utf8))
        XCTAssertEqual(decoded.speed, 1)
        XCTAssertEqual(
            RemoteClock.position(of: decoded, receivedAt: start, now: start.addingTimeInterval(5)),
            15,
            accuracy: 0.001
        )
    }

    func testProgressIsZeroWithoutADuration() {
        let live = RemoteSession(id: "s1", videoId: "v1", position: 30, duration: 0)
        XCTAssertEqual(RemoteClock.progress(of: live, receivedAt: start, now: start), 0)
    }
}

final class RemotePublishRuleTests: XCTestCase {
    private let start = Date(timeIntervalSince1970: 2_000_000)

    private func session(position: Double, paused: Bool = false, title: String = "A Video") -> RemoteSession {
        RemoteSession(id: "s1", videoId: "v1", title: title, position: position, duration: 600, paused: paused)
    }

    func testFirstStateAlwaysPublishes() {
        XCTAssertTrue(RemotePublishRule.shouldPublish(
            previous: nil, sent: nil, next: session(position: 0), now: start
        ))
    }

    /// The common case, several times a second: the clock advanced by exactly
    /// as much as a controller would have projected, so there is nothing to
    /// say.
    func testOrdinaryPlaybackStaysQuiet() {
        XCTAssertFalse(RemotePublishRule.shouldPublish(
            previous: session(position: 100),
            sent: start,
            next: session(position: 103),
            now: start.addingTimeInterval(3)
        ))
    }

    func testPauseIsPublishedAtOnce() {
        XCTAssertTrue(RemotePublishRule.shouldPublish(
            previous: session(position: 100),
            sent: start,
            next: session(position: 101, paused: true),
            now: start.addingTimeInterval(1)
        ))
    }

    func testASeekIsPublishedAtOnce() {
        XCTAssertTrue(RemotePublishRule.shouldPublish(
            previous: session(position: 100),
            sent: start,
            next: session(position: 400),
            now: start.addingTimeInterval(1)
        ))
    }

    func testANewVideoIsPublishedAtOnce() {
        let next = RemoteSession(id: "s1", videoId: "v2", title: "Another", position: 0, duration: 300)
        XCTAssertTrue(RemotePublishRule.shouldPublish(
            previous: session(position: 100), sent: start, next: next, now: start.addingTimeInterval(1)
        ))
    }

    /// Even with nothing to say, the session has to be kept alive or the server
    /// retires it and the phone loses the screen.
    func testHeartbeatPublishesEventually() {
        let elapsed = RemotePublishRule.heartbeat
        XCTAssertTrue(RemotePublishRule.shouldPublish(
            previous: session(position: 100),
            sent: start,
            next: session(position: 100 + elapsed),
            now: start.addingTimeInterval(elapsed)
        ))
    }
}

final class RemoteControlTests: XCTestCase {
    private let baseURL = URL(string: "https://flimm.example.com")!

    private static let listing = """
    {
      "sessions": [
        {
          "id": "1f0b3d2e-2c3a-4f5b-8a1d-6e7f80912345",
          "device": "Living Room",
          "platform": "tvos",
          "video_id": "yt-id",
          "title": "Input shaping, finally explained",
          "channel_name": "A Channel",
          "thumb_url": "/media/thumb/video/yt-id",
          "position": 561,
          "duration": 1476,
          "paused": false,
          "speed": 1,
          "audio_only": false,
          "can_next": true,
          "can_previous": false,
          "updated_at": "2026-09-01T19:04:11Z"
        }
      ],
      "version": 12
    }
    """

    private func client(_ session: URLSession) -> APIClient {
        APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)
    }

    /// The list answers once and then refuses, which is what stops the poll
    /// loop spinning inside a test. The refusal is the same thing a server
    /// being restarted looks like.
    private func pollingSession() -> URLSession {
        let calls = CallCounter()
        return StubURLProtocol.session { request, _ in
            guard request.url?.path == "/api/v1/playback/sessions" else { return (500, Data("{}".utf8)) }
            return calls.next() == 0 ? (200, Data(Self.listing.utf8)) : (500, Data("{}".utf8))
        }
    }

    @MainActor
    func testPollPopulatesTheSessions() async throws {
        let control = RemoteControl(client: client(pollingSession()))
        control.start()
        defer { control.stop() }

        try await waitFor { control.current != nil }
        let current = try XCTUnwrap(control.current)
        XCTAssertEqual(current.device, "Living Room")
        XCTAssertEqual(current.videoId, "yt-id")
        XCTAssertTrue(current.canNext)
        XCTAssertFalse(current.canPrevious)
        // The first ask carries no version; the poll that follows it does, or
        // the controller is polling on a timer rather than waiting on a change.
        try await waitFor { StubURLProtocol.recorded.count >= 2 }
        XCTAssertEqual(StubURLProtocol.recorded.first?.query, nil)
        XCTAssertEqual(StubURLProtocol.recorded.dropFirst().first?.query, "since=12")
    }

    /// A play button that waits a round trip to change reads as one that did
    /// not work, and gets pressed twice.
    @MainActor
    func testPauseIsEchoedBeforeTheServerAnswers() async throws {
        let control = RemoteControl(client: client(pollingSession()))
        control.start()
        defer { control.stop() }
        try await waitFor { control.current != nil }
        XCTAssertEqual(control.current?.paused, false)

        await control.send(.pause)

        XCTAssertEqual(control.current?.paused, true)
        let command = try XCTUnwrap(StubURLProtocol.recorded.last { $0.method == "POST" })
        XCTAssertEqual(command.path, "/api/v1/playback/sessions/1f0b3d2e-2c3a-4f5b-8a1d-6e7f80912345/commands")
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(command.body)) as? [String: Any])
        XCTAssertEqual(body["kind"] as? String, "pause")
    }

    /// A skip is sent as a delta, never as a seek computed here: the television
    /// is the only side that knows where it actually is.
    @MainActor
    func testSkipTravelsAsADelta() async throws {
        let control = RemoteControl(client: client(pollingSession()))
        control.start()
        defer { control.stop() }
        try await waitFor { control.current != nil }

        await control.skip(-10)

        let command = try XCTUnwrap(StubURLProtocol.recorded.last { $0.method == "POST" })
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(command.body)) as? [String: Any])
        XCTAssertEqual(body["kind"] as? String, "skip")
        XCTAssertEqual(body["delta"] as? Double, -10)
        XCTAssertEqual(control.current?.position ?? 0, 551, accuracy: 1)
    }

    /// Stepping is the player's decision to make; a controller that offered it
    /// for a video with nothing after it would send a command nothing answers.
    @MainActor
    func testStepIsRefusedWhenThePlayerSaysThereIsNowhereToGo() async throws {
        let control = RemoteControl(client: client(pollingSession()))
        control.start()
        defer { control.stop() }
        try await waitFor { control.current != nil }

        await control.goPrevious()

        XCTAssertNil(StubURLProtocol.recorded.last { $0.method == "POST" })
    }
}

final class RemotePublisherTests: XCTestCase {
    private let baseURL = URL(string: "https://flimm.example.com")!

    @MainActor
    func testPublishesOnStartAndAppliesACommand() async throws {
        let calls = CallCounter()
        let session = StubURLProtocol.session { request, _ in
            guard let path = request.url?.path else { return (500, Data("{}".utf8)) }
            if path.hasSuffix("/commands") {
                // One pause, then refuse — enough to prove it is applied
                // without leaving the poll loop spinning through the test.
                return calls.next() == 0
                    ? (200, Data(#"{"commands":[{"seq":1,"kind":"pause"}],"cursor":1}"#.utf8))
                    : (500, Data("{}".utf8))
            }
            return (204, Data())
        }
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)
        let publisher = RemotePublisher(client: client, device: "Living Room", platform: "tvos")

        let applied = Box<RemoteCommand?>(nil)
        publisher.start(
            state: { RemoteSession(id: "", videoId: "yt-id", title: "A Video", position: 42, duration: 600) },
            onCommand: { applied.value = $0 }
        )
        defer { Task { await publisher.stop() } }

        try await waitFor { applied.value != nil }
        XCTAssertEqual(applied.value?.action, .pause)

        let put = try XCTUnwrap(StubURLProtocol.recorded.first { $0.method == "PUT" })
        XCTAssertEqual(put.path, "/api/v1/playback/sessions/\(publisher.sessionId)")
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(put.body)) as? [String: Any])
        XCTAssertEqual(body["video_id"] as? String, "yt-id")
        XCTAssertEqual(body["device"] as? String, "Living Room")
        XCTAssertEqual(body["platform"] as? String, "tvos")
        XCTAssertEqual(body["position"] as? Double, 42)
    }

    /// A command kind this player has never heard of is a newer controller
    /// talking, and must be skipped rather than guessed at.
    @MainActor
    func testUnknownCommandsAreIgnored() async throws {
        let calls = CallCounter()
        let session = StubURLProtocol.session { request, _ in
            guard request.url?.path.hasSuffix("/commands") == true else { return (204, Data()) }
            return calls.next() == 0
                ? (200, Data(#"{"commands":[{"seq":1,"kind":"teleport"}],"cursor":1}"#.utf8))
                : (500, Data("{}".utf8))
        }
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)
        let publisher = RemotePublisher(client: client, device: "Living Room", platform: "tvos")

        let applied = Box<RemoteCommand?>(nil)
        publisher.start(
            state: { RemoteSession(id: "", videoId: "yt-id", position: 0, duration: 600) },
            onCommand: { applied.value = $0 }
        )
        defer { Task { await publisher.stop() } }

        // Wait for the poll to have been answered, then assert nothing landed.
        try await waitFor { StubURLProtocol.recorded.contains { $0.path?.hasSuffix("/commands") == true } }
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertNil(applied.value)
    }
}

// MARK: - Helpers

/// Counts calls from the stub's handler, which is not isolated to an actor.
private final class CallCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0

    func next() -> Int {
        lock.withLock {
            defer { count += 1 }
            return count
        }
    }
}

/// A mutable slot a `@MainActor` callback can write and a test can read.
@MainActor
private final class Box<T> {
    var value: T
    init(_ value: T) { self.value = value }
}

/// Waits for a condition instead of for a stretch of wall clock: a loaded CI
/// machine is slower than any fixed sleep that is still quick here.
private func waitFor(
    timeout: Duration = .seconds(2),
    _ condition: @MainActor () -> Bool,
    file: StaticString = #filePath,
    line: UInt = #line
) async throws {
    let deadline = ContinuousClock.now + timeout
    while ContinuousClock.now < deadline {
        if await MainActor.run(body: condition) { return }
        try await Task.sleep(for: .milliseconds(5))
    }
    XCTFail("condition was never met", file: file, line: line)
}
