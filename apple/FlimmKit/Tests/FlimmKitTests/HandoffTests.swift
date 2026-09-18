import XCTest
@testable import FlimmKit

final class HandoffTests: XCTestCase {
    private let server = URL(string: "https://flimm.example.com")!

    // MARK: - The wire

    func testWatchRoundTrip() throws {
        let continuation = Continuation(
            server: server,
            destination: .watch(
                videoId: "yt-1",
                position: 412.6,
                context: .playlist("PL-1", shuffleSeed: "seed-1", audioOnly: true)
            ),
            title: "Making a thing"
        )
        let back = try XCTUnwrap(Continuation(userInfo: continuation.userInfo))
        XCTAssertEqual(back.server, server)
        XCTAssertEqual(back.title, "Making a thing")
        guard case .watch(let id, let position, let context) = back.destination else {
            return XCTFail("expected a watch destination")
        }
        XCTAssertEqual(id, "yt-1")
        // Whole seconds on the wire: nothing downstream can use more, and the
        // dictionary is broadcast to every device in the room.
        XCTAssertEqual(position, 413)
        XCTAssertEqual(context, .playlist("PL-1", shuffleSeed: "seed-1", audioOnly: true))
    }

    func testWatchWithoutContextCarriesNoParameters() throws {
        let continuation = Continuation(
            server: server,
            destination: .watch(videoId: "yt-1", position: 0, context: .none),
            title: "Solo"
        )
        XCTAssertNil(continuation.userInfo["feed"])
        XCTAssertNil(continuation.userInfo["playlist"])
        XCTAssertNil(continuation.userInfo["shuffle"])
        let back = try XCTUnwrap(Continuation(userInfo: continuation.userInfo))
        guard case .watch(_, _, let context) = back.destination else { return XCTFail("expected a watch destination") }
        XCTAssertEqual(context, .none)
    }

    func testPageRoundTrip() throws {
        let pages: [Continuation.Page] = [.feed("feed-1"), .channel("UC-1"), .playlist("PL-1"), .history, .stats]
        for page in pages {
            let continuation = Continuation(server: server, destination: .page(page), title: "Anything")
            let back = try XCTUnwrap(Continuation(userInfo: continuation.userInfo), "\(page)")
            XCTAssertEqual(back.destination, .page(page))
        }
    }

    /// An id with a colon in it must not split into the wrong page.
    func testPageTokenSplitsOnTheFirstColonOnly() throws {
        let continuation = Continuation(server: server, destination: .page(.feed("a:b:c")), title: "Feed")
        let back = try XCTUnwrap(Continuation(userInfo: continuation.userInfo))
        XCTAssertEqual(back.destination, .page(.feed("a:b:c")))
    }

    /// What a newer app handing off something this one has never heard of
    /// looks like: nothing, rather than a screen picked at random.
    func testUnknownPayloadIsRefused() {
        XCTAssertNil(Continuation(userInfo: [:]))
        XCTAssertNil(Continuation(userInfo: ["server": "https://flimm.example.com", "kind": "podcast"]))
        XCTAssertNil(Continuation(userInfo: ["server": "https://flimm.example.com", "kind": "page", "page": "queue:1"]))
        XCTAssertNil(Continuation(userInfo: ["kind": "watch", "video": "yt-1"]))
        XCTAssertNil(Continuation(userInfo: ["server": "https://flimm.example.com", "kind": "watch"]))
    }

    // MARK: - The web address

    func testWatchWebpageURLIsTheWebClientsOwnLink() {
        let continuation = Continuation(
            server: server,
            destination: .watch(videoId: "yt-1", position: 412.6, context: .feed("feed-1")),
            title: "Making a thing"
        )
        XCTAssertEqual(
            continuation.webpageURL?.absoluteString,
            "https://flimm.example.com/watch/yt-1?feed=feed-1&t=413"
        )
    }

    func testWatchAtTheStartCarriesNoTimestamp() {
        let continuation = Continuation(
            server: server,
            destination: .watch(videoId: "yt-1", position: 0.2, context: .none),
            title: "Making a thing"
        )
        XCTAssertEqual(continuation.webpageURL?.absoluteString, "https://flimm.example.com/watch/yt-1")
    }

    func testPageWebpageURLs() {
        let cases: [(Continuation.Page, String)] = [
            (.feed("feed-1"), "https://flimm.example.com/feeds/feed-1"),
            (.channel("UC-1"), "https://flimm.example.com/channels/UC-1"),
            (.playlist("PL-1"), "https://flimm.example.com/playlists/PL-1"),
            (.history, "https://flimm.example.com/history"),
            (.stats, "https://flimm.example.com/stats")
        ]
        for (page, expected) in cases {
            let continuation = Continuation(server: server, destination: .page(page), title: "")
            XCTAssertEqual(continuation.webpageURL?.absoluteString, expected, "\(page)")
        }
    }

    /// A server reached on a sub-path keeps it — the web client is served from
    /// wherever the API is, and the link has to land in the same place.
    func testSubPathServerKeepsItsPrefix() {
        let continuation = Continuation(
            server: URL(string: "https://example.com/flimm/")!,
            destination: .page(.history),
            title: ""
        )
        XCTAssertEqual(continuation.webpageURL?.absoluteString, "https://example.com/flimm/history")
    }

    // MARK: - Whose server

    func testServerMatchIgnoresATrailingSlash() {
        let continuation = Continuation(server: server, destination: .page(.history), title: "")
        XCTAssertTrue(continuation.matches(server: URL(string: "https://flimm.example.com/")!))
        XCTAssertTrue(continuation.matches(server: URL(string: "https://FLIMM.example.com")!))
        XCTAssertFalse(continuation.matches(server: URL(string: "https://other.example.com")!))
    }

    // MARK: - When to speak

    func testFirstStateAlwaysPublishes() {
        XCTAssertTrue(HandoffRule.shouldPublish(nil, watching(at: 0)))
    }

    func testPlaybackPublishesOnlyOnceItHasMoved() {
        let published = watching(at: 100)
        XCTAssertFalse(HandoffRule.shouldPublish(published, watching(at: 104)))
        XCTAssertTrue(HandoffRule.shouldPublish(published, watching(at: 110)))
        // A seek backwards moves as much as one forwards.
        XCTAssertTrue(HandoffRule.shouldPublish(published, watching(at: 80)))
    }

    func testAnythingButThePositionPublishesAtOnce() {
        let published = watching(at: 100)
        XCTAssertTrue(HandoffRule.shouldPublish(published, watching(at: 100, videoId: "yt-2")))
        XCTAssertTrue(HandoffRule.shouldPublish(published, watching(at: 100, context: .feed("feed-2"))))
        XCTAssertTrue(HandoffRule.shouldPublish(published, watching(at: 100, title: "Something else")))
        XCTAssertTrue(HandoffRule.shouldPublish(
            published,
            Continuation(server: server, destination: .page(.history), title: "History")
        ))
    }

    func testPageOnlyPublishesWhenItChanges() {
        let published = Continuation(server: server, destination: .page(.feed("feed-1")), title: "Making")
        XCTAssertFalse(HandoffRule.shouldPublish(published, published))
        XCTAssertTrue(HandoffRule.shouldPublish(
            published,
            Continuation(server: server, destination: .page(.feed("feed-2")), title: "Making")
        ))
    }

    private func watching(
        at position: Double,
        videoId: String = "yt-1",
        context: PlaybackContext = .feed("feed-1"),
        title: String = "Making a thing"
    ) -> Continuation {
        Continuation(
            server: server,
            destination: .watch(videoId: videoId, position: position, context: context),
            title: title
        )
    }
}
