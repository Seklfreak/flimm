import XCTest
@testable import FlimmKit

final class PlaybackContextTests: XCTestCase {
    /// The parameters, and their order, are the web client's — a link shared
    /// between a phone and a browser has to resolve to the same run.
    func testQueryItemOrder() {
        let context = PlaybackContext.playlist("PL-1", shuffleSeed: "seed-1", audioOnly: true)
        XCTAssertEqual(context.queryItems.map(\.name), ["playlist", "shuffle", "audio"])
        XCTAssertEqual(context.queryString, "?playlist=PL-1&shuffle=seed-1&audio=1")
    }

    func testAudioOnlyIsOmittedWhenOff() {
        XCTAssertEqual(PlaybackContext.feed("feed-1").queryString, "?feed=feed-1")
        XCTAssertEqual(PlaybackContext.channel("UC-1").queryString, "?channel=UC-1")
    }

    func testNoContextProducesNoQuery() {
        XCTAssertTrue(PlaybackContext.none.queryItems.isEmpty)
        XCTAssertEqual(PlaybackContext.none.queryString, "")
    }

    func testRoundTripThroughQueryItems() {
        let cases: [PlaybackContext] = [
            .none,
            .feed("feed-1"),
            .channel("UC-1", audioOnly: true),
            .playlist("PL-1", shuffleSeed: "seed-1"),
            .playlist("PL-music", shuffleSeed: "seed-2", audioOnly: true)
        ]
        for context in cases {
            XCTAssertEqual(PlaybackContext(queryItems: context.queryItems), context, "\(context)")
        }
    }

    func testParsesADeepLinkAndIgnoresUnknownParameters() throws {
        let url = URL(string: "flimm://watch/yt-id?playlist=PL-1&shuffle=abc&audio=1&t=400&utm=x")!
        let items = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems)
        let context = PlaybackContext(queryItems: items)
        XCTAssertEqual(context.source, .playlist("PL-1"))
        XCTAssertEqual(context.shuffleSeed, "abc")
        XCTAssertTrue(context.audioOnly)
        XCTAssertTrue(context.isShuffled)
    }

    /// The heartbeat needs the playlist it is playing *from*, and only that.
    func testPlaylistIdIsOnlySetForAPlaylistContext() {
        XCTAssertEqual(PlaybackContext.playlist("PL-1").playlistId, "PL-1")
        XCTAssertNil(PlaybackContext.feed("feed-1").playlistId)
        XCTAssertNil(PlaybackContext.channel("UC-1").playlistId)
        XCTAssertNil(PlaybackContext.none.playlistId)
    }

    func testShuffleSeedIsFreshEachTime() {
        let first = PlaybackContext.newShuffleSeed()
        let second = PlaybackContext.newShuffleSeed()
        XCTAssertNotEqual(first, second)
        XCTAssertFalse(first.isEmpty)
        // Reshuffling means a new seed; the same seed always means the same order.
        XCTAssertTrue(PlaybackContext.playlist("PL-1", shuffleSeed: first).isShuffled)
        XCTAssertFalse(PlaybackContext.playlist("PL-1", shuffleSeed: "").isShuffled)
    }

    func testEmptySeedIsNotCarried() {
        XCTAssertEqual(PlaybackContext.playlist("PL-1", shuffleSeed: "").queryString, "?playlist=PL-1")
    }
}
