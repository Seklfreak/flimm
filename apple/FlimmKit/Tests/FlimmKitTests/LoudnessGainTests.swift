import XCTest
@testable import FlimmKit

final class LoudnessGainTests: XCTestCase {
    func testDecibelsBecomeThePlayersVolume() {
        XCTAssertEqual(LoudnessGain.volume(forGainDB: -6), 0.501, accuracy: 0.001)
        XCTAssertEqual(LoudnessGain.volume(forGainDB: -20), 0.1, accuracy: 0.001)
        XCTAssertEqual(LoudnessGain.volume(forGainDB: 0), 1)
    }

    /// `AVPlayer.volume` stops at 1, so a boost would silently do nothing —
    /// and a client that pretended otherwise would disagree with the web about
    /// how loud a video is.
    func testAGainIsNeverAppliedAsABoost() {
        XCTAssertEqual(LoudnessGain.volume(forGainDB: 6), 1)
        XCTAssertEqual(LoudnessGain.volume(forGainDB: .nan), 1)
        XCTAssertEqual(LoudnessGain.volume(forGainDB: .infinity), 1)
    }

    /// The response spells its keys with acronyms that `.convertFromSnakeCase`
    /// gets wrong; decoding as 0 would look exactly like "not measured yet".
    func testTheResponseDecodesItsAcronymKeys() throws {
        let json = """
        {"state":"done","gain_db":-6.8,"target_lufs":-18,"measured_lufs":-11.2,"peak_dbtp":-1.5,"range_lu":6.1}
        """
        let info = try FlimmCoding.decoder.decode(LoudnessInfo.self, from: Data(json.utf8))
        XCTAssertEqual(info.state, .done)
        XCTAssertEqual(info.gainDB, -6.8, accuracy: 0.001)
        XCTAssertEqual(info.targetLUFS, -18)
        XCTAssertEqual(info.measuredLUFS, -11.2, accuracy: 0.001)
        XCTAssertEqual(info.peakDBTP, -1.5, accuracy: 0.001)
        XCTAssertEqual(info.rangeLU, 6.1, accuracy: 0.001)
        XCTAssertEqual(LoudnessGain.volume(forGainDB: info.gainDB), 0.457, accuracy: 0.001)
    }

    /// A server that predates the feature, or a measurement still running:
    /// both mean "play it as it was archived".
    func testAnAnswerWithNoNumbersLeavesTheVideoAlone() throws {
        let info = try FlimmCoding.decoder.decode(LoudnessInfo.self, from: Data(#"{"state":"running"}"#.utf8))
        XCTAssertEqual(info.gainDB, 0)
        XCTAssertEqual(LoudnessGain.volume(forGainDB: info.gainDB), 1)
    }

    func testNormalisationIsOnUnlessTheViewerTurnsItOff() {
        XCTAssertTrue(Prefs().normalizeLoudness)
    }
}
