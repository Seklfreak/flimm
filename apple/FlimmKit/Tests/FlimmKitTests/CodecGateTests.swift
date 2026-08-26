import XCTest
@testable import FlimmKit

/// The gate the iOS and tvOS players both consult before building an
/// `AVPlayer`. Getting it wrong in either direction is bad: a false positive
/// refuses a video that plays, a false negative shows a spinner forever.
final class CodecGateTests: XCTestCase {
    private func video(_ json: String) throws -> Video {
        try FlimmCoding.decoder.decode(Video.self, from: Data(json.utf8))
    }

    private func video(streams: String) throws -> Video {
        let patched = Fixtures.videoDetail.replacingOccurrences(
            of: """
            "streams": [ { "type": "video", "codec": "avc1", "width": 1920, "height": 1080, "bitrate": 4500000 },
                           { "type": "audio", "codec": "mp4a", "width": 0, "height": 0, "bitrate": 130000 } ],
            """,
            with: "\"streams\": \(streams),"
        )
        return try video(patched)
    }

    func testH264PlaysDirectly() throws {
        XCTAssertNil(CodecGate.issue(for: try video(Fixtures.videoDetail)))
    }

    func testVP9IsRefusedByName() throws {
        let detail = try video(streams: #"[{"type":"video","codec":"vp09.00.40.08"},{"type":"audio","codec":"opus"}]"#)
        let issue = try XCTUnwrap(CodecGate.issue(for: detail))
        XCTAssertEqual(issue.videoCodec, "vp09.00.40.08")
        XCTAssertTrue(issue.message.contains("vp09"))
        // The AAC rendition is derived from whatever the source audio is, so
        // Opus in the archive does not rule audio-only out.
        XCTAssertTrue(issue.audioAvailable)
    }

    /// A backend that predates `audio_aac_url` has no audio fallback to offer,
    /// whatever the archived audio codec happens to be.
    func testAudioFallbackNeedsTheDerivedRendition() throws {
        let source = Fixtures.videoDetailWithoutAudioAac.replacingOccurrences(of: "\"avc1\"", with: "\"av01.0.05M.08\"")
        let issue = try XCTUnwrap(CodecGate.issue(for: try video(source)))
        XCTAssertFalse(issue.audioAvailable)
    }

    /// `streams` arrived with a later backend release. Its absence means
    /// "unknown", which must not read as "unplayable".
    func testAServerWithoutStreamsIsNotGated() throws {
        XCTAssertNil(CodecGate.issue(for: try video(Fixtures.videoDetailWithoutStreams)))
    }

    /// Audio-only never touches the video track, so the gate has nothing to say.
    func testAudioOnlySidestepsTheGate() throws {
        let detail = try video(streams: #"[{"type":"video","codec":"vp09"},{"type":"audio","codec":"opus"}]"#)
        XCTAssertNil(CodecGate.issue(for: detail, audioOnly: true))
    }

    /// One playable rendition is enough — the player picks it.
    func testAnyPlayableVideoStreamClearsTheGate() throws {
        let detail = try video(streams: #"[{"type":"video","codec":"vp09"},{"type":"video","codec":"avc1.640028"}]"#)
        XCTAssertNil(CodecGate.issue(for: detail))
    }
}
