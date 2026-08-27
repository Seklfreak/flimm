import XCTest
@testable import FlimmKit

/// The gate the iOS and tvOS players both consult before building an
/// `AVPlayer`. Getting it wrong in either direction is bad: a false positive
/// refuses a video that plays, a false negative shows a spinner forever — and
/// reaching for a rendition when the archive would have played burns a core on
/// the server for nothing.
final class CodecGateTests: XCTestCase {
    /// A current phone: ~1200 px tall in portrait, HEVC in hardware.
    private let phone = DeviceCapabilities(screenHeight: 1206, decodesHEVC: true)
    private let tv4K = DeviceCapabilities(screenHeight: 2160, decodesHEVC: true)
    private let tvHD = DeviceCapabilities(screenHeight: 1080, decodesHEVC: true)
    /// Old enough to have no HEVC decoder — the case the `codec` field is for.
    private let noHEVC = DeviceCapabilities(screenHeight: 2160, decodesHEVC: false)

    private let undecodable = #"[{"type":"video","codec":"vp09.00.40.08"},{"type":"audio","codec":"opus"}]"#

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

    private func video(streams: String, from source: String? = nil) throws -> Video {
        try video(patched(source ?? Fixtures.videoDetail, streams: streams))
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

    /// The rendition a decision settled on, or a failure naming what came back.
    private func chosen(_ decision: CodecGate.Decision, file: StaticString = #filePath, line: UInt = #line) throws -> CodecGate.HLSChoice {
        guard case .hls(let choice) = decision else {
            XCTFail("expected a rendition, got \(decision)", file: file, line: line)
            throw XCTSkip("not a rendition")
        }
        return choice
    }

    // MARK: - Which stream plays at all

    func testH264PlaysDirectly() throws {
        XCTAssertEqual(CodecGate.decision(for: try video(Fixtures.videoDetail), device: phone), .native)
    }

    /// The point of the whole feature: a codec this device has no decoder for
    /// is not a refusal any more, it is a rendition.
    func testUndecodableVideoFallsBackToTheCompatibleRendition() throws {
        let detail = try video(streams: undecodable)
        let choice = try chosen(CodecGate.decision(for: detail, device: phone))
        XCTAssertEqual(choice.height, 1080)
        XCTAssertEqual(choice.url, "/media/hls/yt-id/1080/index.m3u8")
        XCTAssertEqual(choice.state, .done)
        XCTAssertEqual(choice.codec, .h264)
    }

    /// `hls_url` is present but empty, and there is no ladder — treat it as
    /// absent rather than handing an empty path to `AVPlayer`.
    func testAnEmptyHLSURLIsNotARendition() throws {
        let source = Fixtures.videoDetailWithoutVariants
            .replacingOccurrences(of: "\"/media/hls/yt-id/1080/index.m3u8\"", with: "\"\"")
        guard case .audioOnly = CodecGate.decision(for: try video(streams: undecodable, from: source), device: phone) else {
            return XCTFail("an empty hls_url must not be offered as a rendition")
        }
    }

    /// A backend without `hls_url` still has the derived AAC audio, so the
    /// wall offers listening rather than nothing.
    func testALegacyServerFallsBackToAudioOnly() throws {
        let detail = try legacyVideo(streams: undecodable)
        guard case .audioOnly(let issue) = CodecGate.decision(for: detail, device: phone) else {
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
        guard case .unplayable(let issue) = CodecGate.decision(for: detail, device: phone) else {
            return XCTFail("expected an outright refusal")
        }
        XCTAssertEqual(issue.videoCodec, "av01.0.05M.08")
        XCTAssertFalse(issue.audioAvailable)
    }

    /// `streams` arrived with a later backend release. Its absence means
    /// "unknown", which must not read as "unplayable" — nor cost a transcode.
    func testAServerWithoutStreamsIsNotGated() throws {
        XCTAssertEqual(CodecGate.decision(for: try video(Fixtures.videoDetailWithoutStreams), device: phone), .native)
    }

    /// Audio-only never touches the video track, so the gate has nothing to
    /// say — not even when a height was asked for.
    func testAudioOnlySidestepsTheGate() throws {
        let detail = try video(streams: undecodable)
        XCTAssertEqual(CodecGate.decision(for: detail, audioOnly: true, device: phone), .native)
        XCTAssertEqual(CodecGate.decision(for: detail, preference: .height(480), audioOnly: true, device: phone), .native)
    }

    /// One playable rendition is enough — the player picks it, and the server
    /// is spared the transcode.
    func testAnyPlayableVideoStreamClearsTheGate() throws {
        let detail = try video(streams: #"[{"type":"video","codec":"vp09"},{"type":"video","codec":"avc1.640028"}]"#)
        XCTAssertEqual(CodecGate.decision(for: detail, device: phone), .native)
    }

    /// The same question the quality menu asks to decide whether to name the
    /// source at all. Unknown `streams` counts as playable, exactly as the
    /// gate treats it.
    func testArchivePlaysMirrorsTheGate() throws {
        XCTAssertTrue(CodecGate.archivePlays(try video(Fixtures.videoDetail), on: phone))
        XCTAssertTrue(CodecGate.archivePlays(try video(Fixtures.videoDetailWithoutStreams), on: phone))
        XCTAssertFalse(CodecGate.archivePlays(try video(streams: undecodable), on: phone))
    }

    // MARK: - The quality rule

    /// Auto over a playable archive is the archived file: full quality, and
    /// nothing for the server to do.
    func testAutoPrefersThePlayableArchive() throws {
        XCTAssertEqual(CodecGate.decision(for: try video(Fixtures.videoDetail), preference: .auto, device: tv4K), .native)
    }

    /// The subtle one, and it is intended: a picked height wins even over an
    /// archive that would have played. "720p" is a request for less data.
    func testAnExplicitHeightBeatsAPlayableArchive() throws {
        let choice = try chosen(CodecGate.decision(for: try video(Fixtures.videoDetail), preference: .height(720), device: phone))
        XCTAssertEqual(choice.height, 720)
        XCTAssertEqual(choice.url, "/media/hls/yt-id/720/index.m3u8")
        XCTAssertEqual(choice.state, .pending)
    }

    /// A 4K TV gets the 4K rung; that is what the HEVC rungs exist for.
    func testAutoPicksTheTallestRungTheScreenCanShow() throws {
        let detail = try video(streams: undecodable, from: Fixtures.videoDetail4K)
        let choice = try chosen(CodecGate.decision(for: detail, device: tv4K))
        XCTAssertEqual(choice.height, 2160)
        XCTAssertEqual(choice.codec, .hevc)
    }

    /// The same video on an HD screen: 4K would be bandwidth and server time
    /// spent on pixels nobody sees.
    func testAutoStopsAtTheScreenHeight() throws {
        let detail = try video(streams: undecodable, from: Fixtures.videoDetail4K)
        XCTAssertEqual(try chosen(CodecGate.decision(for: detail, device: tvHD)).height, 1080)
        // A phone is taller than 1080 in pixels but not by a rung.
        XCTAssertEqual(try chosen(CodecGate.decision(for: detail, device: phone)).height, 1080)
    }

    /// No HEVC decoder rules out 2160 and 1440 whatever the screen says.
    func testAutoSkipsHEVCOnADeviceThatCannotDecodeIt() throws {
        let detail = try video(streams: undecodable, from: Fixtures.videoDetail4K)
        let choice = try chosen(CodecGate.decision(for: detail, device: noHEVC))
        XCTAssertEqual(choice.height, 1080)
        XCTAssertEqual(choice.codec, .h264)
    }

    /// Asking for 4K on a 1080p source is not a refusal: the ladder stops at
    /// the source height, so the nearest lower rung is the answer.
    func testAnUnofferedHeightFallsToTheNearestLower() throws {
        let detail = try video(streams: undecodable)
        XCTAssertEqual(try chosen(CodecGate.decision(for: detail, preference: .height(2160), device: tv4K)).height, 1080)
        XCTAssertEqual(try chosen(CodecGate.decision(for: detail, preference: .height(1440), device: tv4K)).height, 1080)
    }

    func testAnOfferedHeightIsTakenExactly() throws {
        let detail = try video(streams: undecodable)
        XCTAssertEqual(try chosen(CodecGate.decision(for: detail, preference: .height(480), device: tv4K)).height, 480)
    }

    /// Below the smallest rung there is still a rung — 480 is always offered.
    func testAHeightBelowEveryRungFallsToTheSmallest() throws {
        let detail = try video(streams: undecodable)
        XCTAssertEqual(try chosen(CodecGate.decision(for: detail, preference: .height(240), device: phone)).height, 480)
    }

    /// A picked height that is HEVC-only on a device without the decoder skips
    /// down the ladder rather than playing nothing.
    func testAPickedHEVCHeightFallsToAPlayableRung() throws {
        let detail = try video(streams: undecodable, from: Fixtures.videoDetail4K)
        XCTAssertEqual(try chosen(CodecGate.decision(for: detail, preference: .height(2160), device: noHEVC)).height, 1080)
    }

    // MARK: - Backends without the ladder

    /// `hls_variants` arrives after `hls_url`. Between the two releases there
    /// is one rendition and no height to name it by.
    func testAServerWithoutTheLadderStillPlaysItsOneRendition() throws {
        let detail = try video(streams: undecodable, from: Fixtures.videoDetailWithoutVariants)
        let choice = try chosen(CodecGate.decision(for: detail, preference: .height(480), device: phone))
        XCTAssertEqual(choice.url, "/media/hls/yt-id/1080/index.m3u8")
        XCTAssertNil(choice.height)
        XCTAssertNil(choice.state)
    }

    /// …and a height cannot be honoured there at all, so a playable archive
    /// stays the answer rather than being swapped for a rendition of unknown
    /// size.
    func testAHeightWithoutALadderKeepsThePlayableArchive() throws {
        let detail = try video(Fixtures.videoDetailWithoutVariants)
        XCTAssertEqual(CodecGate.decision(for: detail, preference: .height(720), device: phone), .native)
    }

    /// Every rung in a codec this device lacks: the default rendition is the
    /// last thing to try before the wall.
    func testALadderThisDeviceCannotDecodeFallsBackToTheDefaultRendition() throws {
        let source = Fixtures.videoDetail.replacingOccurrences(of: "\"codec\": \"h264\"", with: "\"codec\": \"hevc\"")
        let detail = try video(streams: undecodable, from: source)
        let choice = try chosen(CodecGate.decision(for: detail, device: noHEVC))
        XCTAssertEqual(choice.url, "/media/hls/yt-id/1080/index.m3u8")
        XCTAssertNil(choice.variant)
    }
}

extension CodecGateTests {
    func testExplicitHeightAtOrAbovePlayableSourceUsesArchive() throws {
        let video = try FlimmCoding.decoder.decode(Video.self, from: Data(Fixtures.videoDetail.utf8))
        // Fixture: H.264 1080p source with a variant ladder.
        let device = DeviceCapabilities(screenHeight: 2160, decodesHEVC: true)
        XCTAssertEqual(CodecGate.decision(for: video, preference: .height(2160), device: device), .native)
        XCTAssertEqual(CodecGate.decision(for: video, preference: .height(1080), device: device), .native)
        if case .hls(let choice) = CodecGate.decision(for: video, preference: .height(720), device: device) {
            XCTAssertEqual(choice.variant?.height, 720)
        } else {
            XCTFail("a lower pick still selects a rendition")
        }
    }
}

extension CodecGateTests {
    /// The archive check is a property of the device value, not of the
    /// machine running the tests: an AV1-capable phone plays the source.
    func testADeviceWithAV1DecodePlaysTheArchive() throws {
        let detail = try legacyVideo(streams: #"[{"type":"video","codec":"av01.0.05M.08"}]"#, audio: false)
        let proPhone = DeviceCapabilities(screenHeight: 1206, decodesHEVC: true, decodesAV1: true)
        XCTAssertEqual(CodecGate.decision(for: detail, device: proPhone), .native)
    }
}
