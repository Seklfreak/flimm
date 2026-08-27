import XCTest
@testable import FlimmKit

/// The gate the iOS and tvOS players both consult before building an
/// `AVPlayer`. Getting it wrong in either direction is bad: a false positive
/// refuses a video that plays, a false negative shows a spinner forever — and
/// reaching for the compatible rendition when the archive would have played
/// burns a core on the server for nothing.
final class CodecGateTests: XCTestCase {
    private func video(_ json: String) throws -> Video {
        try FlimmCoding.decoder.decode(Video.self, from: Data(json.utf8))
    }

    private func patched(_ source: String, streams: String) -> String {
        source.replacingOccurrences(
            of: """
            "streams": [ { "type": "video", "codec": "avc1", "width": 1920, "height": 1080, "bitrate": 4500000 },
                           { "type": "audio", "codec": "mp4a", "width": 0, "height": 0, "bitrate": 130000 } ],
            """,
            with: "\"streams\": \(streams),"
        )
    }

    private func video(streams: String) throws -> Video {
        try video(patched(Fixtures.videoDetail, streams: streams))
    }

    /// The same video on a backend that predates `hls_url`, optionally
    /// predating `audio_aac_url` too.
    private func legacyVideo(streams: String, audio: Bool = true) throws -> Video {
        var source = Fixtures.videoDetailWithoutHLS
        if !audio {
            source = source
                .split(separator: "\n", omittingEmptySubsequences: false)
                .filter { !$0.contains("\"audio_aac_url\"") }
                .joined(separator: "\n")
        }
        return try video(patched(source, streams: streams))
    }

    func testH264PlaysDirectly() throws {
        XCTAssertEqual(CodecGate.decision(for: try video(Fixtures.videoDetail)), .native)
    }

    /// The point of the whole feature: a codec this device has no decoder for
    /// is not a refusal any more, it is the compatible rendition.
    func testUndecodableVideoFallsBackToTheCompatibleRendition() throws {
        let detail = try video(streams: #"[{"type":"video","codec":"vp09.00.40.08"},{"type":"audio","codec":"opus"}]"#)
        XCTAssertEqual(CodecGate.decision(for: detail), .hls("/media/hls/yt-id/index.m3u8"))
    }

    /// `hls_url` is present but empty — treat it as absent rather than
    /// handing an empty path to `AVPlayer`.
    func testAnEmptyHLSURLIsNotARendition() throws {
        let source = patched(
            Fixtures.videoDetail,
            streams: #"[{"type":"video","codec":"av01.0.05M.08"},{"type":"audio","codec":"opus"}]"#
        )
        .replacingOccurrences(of: "\"/media/hls/yt-id/index.m3u8\"", with: "\"\"")
        guard case .audioOnly = CodecGate.decision(for: try video(source)) else {
            return XCTFail("an empty hls_url must not be offered as a rendition")
        }
    }

    /// A backend without `hls_url` still has the derived AAC audio, so the
    /// wall offers listening rather than nothing.
    func testALegacyServerFallsBackToAudioOnly() throws {
        let detail = try legacyVideo(streams: #"[{"type":"video","codec":"vp09.00.40.08"},{"type":"audio","codec":"opus"}]"#)
        guard case .audioOnly(let issue) = CodecGate.decision(for: detail) else {
            return XCTFail("expected the audio-only fallback")
        }
        XCTAssertEqual(issue.videoCodec, "vp09.00.40.08")
        XCTAssertTrue(issue.message.contains("vp09"))
        // The AAC rendition is derived from whatever the source audio is, so
        // Opus in the archive does not rule audio-only out.
        XCTAssertTrue(issue.audioAvailable)
    }

    /// No compatible rendition and no derived audio: the one case that is
    /// still a dead end, and the only one the codec-gate wall is for.
    func testALegacyServerWithoutAudioIsUnplayable() throws {
        let detail = try legacyVideo(streams: #"[{"type":"video","codec":"av01.0.05M.08"}]"#, audio: false)
        guard case .unplayable(let issue) = CodecGate.decision(for: detail) else {
            return XCTFail("expected an outright refusal")
        }
        XCTAssertEqual(issue.videoCodec, "av01.0.05M.08")
        XCTAssertFalse(issue.audioAvailable)
    }

    /// `streams` arrived with a later backend release. Its absence means
    /// "unknown", which must not read as "unplayable" — nor cost a transcode.
    func testAServerWithoutStreamsIsNotGated() throws {
        XCTAssertEqual(CodecGate.decision(for: try video(Fixtures.videoDetailWithoutStreams)), .native)
    }

    /// Audio-only never touches the video track, so the gate has nothing to
    /// say — and must not send the player to the compatible rendition.
    func testAudioOnlySidestepsTheGate() throws {
        let detail = try video(streams: #"[{"type":"video","codec":"vp09"},{"type":"audio","codec":"opus"}]"#)
        XCTAssertEqual(CodecGate.decision(for: detail, audioOnly: true), .native)
    }

    /// One playable rendition is enough — the player picks it, and the server
    /// is spared the transcode.
    func testAnyPlayableVideoStreamClearsTheGate() throws {
        let detail = try video(streams: #"[{"type":"video","codec":"vp09"},{"type":"video","codec":"avc1.640028"}]"#)
        XCTAssertEqual(CodecGate.decision(for: detail), .native)
    }
}
