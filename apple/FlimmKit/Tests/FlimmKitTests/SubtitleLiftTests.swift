import XCTest
@testable import FlimmKit

final class SubtitleLiftTests: XCTestCase {
    /// The bug this exists for: one constant cannot serve both pictures. A
    /// phone's player and an iPad's differ by enough that a margin on one is
    /// the bottom edge on the other.
    func testTheIdleLiftGrowsWithThePicture() {
        let phone = SubtitleLift.idle(pictureHeight: 211)
        let pad = SubtitleLift.idle(pictureHeight: 350)
        XCTAssertGreaterThan(pad, phone + 10)
        XCTAssertEqual(pad, 35, accuracy: 0.001)
    }

    /// A player barely taller than the text still clears its own edge.
    func testAVerySmallPlayerKeepsAMinimum() {
        XCTAssertEqual(SubtitleLift.idle(pictureHeight: 80), SubtitleLift.idleMinimum)
        XCTAssertEqual(SubtitleLift.idle(pictureHeight: 0), SubtitleLift.idleMinimum)
    }

    /// Pausing puts a control bar over the bottom of the picture, and the cue
    /// has to clear *that*, whatever it measures.
    func testChromeIsClearedByItsOwnHeight() {
        XCTAssertEqual(SubtitleLift.overChrome(barHeight: 92, pictureHeight: 350), 104)
        // A taller bar (the iPad's) pushes further, with no constant to update.
        XCTAssertEqual(SubtitleLift.overChrome(barHeight: 108, pictureHeight: 350), 120)
    }

    /// Chrome that has not been measured yet, or measures as almost nothing,
    /// must not drop the cue below where it sits with no chrome at all.
    func testUnmeasuredChromeNeverLowersTheCue() {
        let idle = SubtitleLift.idle(pictureHeight: 350)
        XCTAssertEqual(SubtitleLift.overChrome(barHeight: 0, pictureHeight: 350), idle)
        XCTAssertEqual(SubtitleLift.overChrome(barHeight: 5, pictureHeight: 350), idle)
    }
}
