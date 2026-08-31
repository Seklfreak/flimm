import XCTest
@testable import FlimmKit

final class PlaybackEndTests: XCTestCase {
    func testAutoplayAdvancesWhenThereIsSomewhereToGo() {
        XCTAssertEqual(PlaybackEnd.decide(autoplay: true, hasNext: true), .advance)
    }

    /// Autoplay off is a deliberate "stop here", however much is queued.
    func testAutoplayOffAlwaysFinishes() {
        XCTAssertEqual(PlaybackEnd.decide(autoplay: false, hasNext: true), .finished)
    }

    /// The end of a feed is an ending too — the one most worth saying out
    /// loud, since nothing follows to explain it.
    func testTheEndOfTheListFinishes() {
        XCTAssertEqual(PlaybackEnd.decide(autoplay: true, hasNext: false), .finished)
        XCTAssertEqual(PlaybackEnd.decide(autoplay: false, hasNext: false), .finished)
    }
}
