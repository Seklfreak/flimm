import XCTest
@testable import FlimmKit

final class ScrubPreviewTests: XCTestCase {
    private let track = """
    WEBVTT

    00:00:00.000 --> 00:00:02.000
    sheet.jpg#xywh=0,0,160,90

    00:00:02.000 --> 00:00:04.000
    sheet.jpg#xywh=160,0,160,90

    00:00:04.000 --> 00:00:06.000
    sheet.jpg#xywh=0,90,160,90
    """
    private let trackPath = "/media/preview/vid1/preview.vtt"

    func testEachCueIsARectangleOfTheSheet() {
        let tiles = ScrubPreview.tiles(from: track, trackPath: trackPath)
        XCTAssertEqual(tiles.count, 3)
        XCTAssertEqual(tiles[0].rect, CGRect(x: 0, y: 0, width: 160, height: 90))
        XCTAssertEqual(tiles[2].rect, CGRect(x: 0, y: 90, width: 160, height: 90))
        XCTAssertEqual(tiles[1].start, 2)
    }

    /// The track names the sheet relative to itself, and the app is nowhere
    /// near that directory.
    func testTheSheetIsResolvedAgainstTheTrack() {
        let tiles = ScrubPreview.tiles(from: track, trackPath: trackPath)
        XCTAssertEqual(tiles[0].sheetPath, "/media/preview/vid1/sheet.jpg")
    }

    func testMalformedCuesAreDroppedRatherThanFatal() {
        let tiles = ScrubPreview.tiles(from: """
        WEBVTT

        00:00:00.000 --> 00:00:02.000
        sheet.jpg

        00:00:02.000 --> 00:00:04.000
        sheet.jpg#xywh=1,2,0,90

        00:00:04.000 --> 00:00:06.000
        sheet.jpg#xywh=0,90,160,90
        """, trackPath: trackPath)
        XCTAssertEqual(tiles.count, 1)
        XCTAssertEqual(tiles[0].start, 4)
    }

    func testSomethingThatIsNotATrackIsNoTiles() {
        XCTAssertTrue(ScrubPreview.tiles(from: "", trackPath: trackPath).isEmpty)
        XCTAssertTrue(ScrubPreview.tiles(from: "<html>404</html>", trackPath: trackPath).isEmpty)
    }

    func testTheTileForAMoment() {
        let tiles = ScrubPreview.tiles(from: track, trackPath: trackPath)
        XCTAssertEqual(ScrubPreview.tile(at: 0, in: tiles)?.rect.minX, 0)
        XCTAssertEqual(ScrubPreview.tile(at: 2.5, in: tiles)?.rect.minX, 160)
        XCTAssertEqual(ScrubPreview.tile(at: 5.9, in: tiles)?.rect.minY, 90)
    }

    /// A drag past the last cue holds the last still; a video with no track
    /// has nothing to hold.
    func testPastTheEndHoldsTheLastStill() {
        let tiles = ScrubPreview.tiles(from: track, trackPath: trackPath)
        XCTAssertEqual(ScrubPreview.tile(at: 9_999, in: tiles)?.start, 4)
        XCTAssertNil(ScrubPreview.tile(at: 5, in: []))
    }
}
