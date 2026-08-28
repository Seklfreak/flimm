import XCTest
@testable import FlimmKit

final class ResumeNoticeTests: XCTestCase {
    func testTheOfferStandsForAMinuteOfPlayback() {
        XCTAssertTrue(ResumeNotice.isVisible(resumedFrom: 2847, currentTime: 2847))
        XCTAssertTrue(ResumeNotice.isVisible(resumedFrom: 2847, currentTime: 2847 + 59))
        XCTAssertFalse(ResumeNotice.isVisible(resumedFrom: 2847, currentTime: 2847 + 60))
    }

    /// A paused player does not advance, so pausing to decide does not burn
    /// the window.
    func testAPausedPlayerKeepsTheOffer() {
        XCTAssertTrue(ResumeNotice.isVisible(resumedFrom: 100, currentTime: 100))
    }

    /// Seeking back before the resume point is still "not a minute in".
    func testSeekingBackKeepsItVisible() {
        XCTAssertTrue(ResumeNotice.isVisible(resumedFrom: 100, currentTime: 40))
    }
}
