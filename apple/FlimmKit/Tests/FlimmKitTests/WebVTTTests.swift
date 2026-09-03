import XCTest
@testable import FlimmKit

final class WebVTTTests: XCTestCase {
    private let sample = """
    WEBVTT
    Kind: captions
    Language: en

    00:00:01.000 --> 00:00:03.500
    First line

    2
    00:00:04.000 --> 00:00:06.000 position:50% align:middle
    Second <c.colour>line</c>
    wrapping over two rows

    00:01:00.000 --> 00:01:02.000
    <v Narrator>Third line
    """

    func testParsesCuesTimingsAndText() {
        let cues = WebVTT.parse(sample)
        XCTAssertEqual(cues.count, 3)
        XCTAssertEqual(cues[0].start, 1, accuracy: 0.001)
        XCTAssertEqual(cues[0].end, 3.5, accuracy: 0.001)
        XCTAssertEqual(cues[0].text, "First line")
    }

    /// Cue settings sit after the end timestamp on the same line; taking the
    /// whole remainder as a timestamp would drop the cue.
    func testIgnoresCueSettingsAfterTheEndTimestamp() {
        let cues = WebVTT.parse(sample)
        XCTAssertEqual(cues[1].end, 6, accuracy: 0.001)
    }

    func testKeepsMultipleTextRowsAndStripsTags() {
        let cues = WebVTT.parse(sample)
        XCTAssertEqual(cues[1].text, "Second line\nwrapping over two rows")
        XCTAssertEqual(cues[2].text, "Third line")
    }

    /// A cue cannot hold a bare `&` or `<` — the format makes them escapes —
    /// so a caption saying "salt & pepper" arrives as `salt &amp; pepper` and
    /// used to be drawn on screen exactly like that. A browser never had this
    /// problem; these players parse the file themselves and so own this too.
    func testDecodesTheCharacterReferencesTheFormatRequires() {
        let cues = WebVTT.parse("""
        WEBVTT

        00:00:01.000 --> 00:00:02.000
        salt &amp; pepper

        00:00:03.000 --> 00:00:04.000
        a &lt;tag&gt; and a &quot;quote&quot;

        00:00:05.000 --> 00:00:06.000
        &amp;lt; is how a caption writes a literal escape
        """)
        XCTAssertEqual(cues[0].text, "salt & pepper")
        XCTAssertEqual(cues[1].text, "a <tag> and a \"quote\"")
        // Decoding `&amp;` first would turn this into a bare `<` and then eat
        // the rest of the line as a tag.
        XCTAssertEqual(cues[2].text, "&lt; is how a caption writes a literal escape")
    }

    func testHeaderAndCueNumbersAreNotCues() {
        let cues = WebVTT.parse(sample)
        XCTAssertFalse(cues.contains { $0.text.contains("WEBVTT") })
        XCTAssertFalse(cues.contains { $0.text == "2" })
    }

    func testCueLookupIsHalfOpen() {
        let cues = WebVTT.parse(sample)
        XCTAssertNil(WebVTT.cue(at: 0.5, in: cues))
        XCTAssertEqual(WebVTT.cue(at: 1, in: cues)?.text, "First line")
        XCTAssertNil(WebVTT.cue(at: 3.5, in: cues), "the end timestamp is exclusive")
        XCTAssertEqual(WebVTT.cue(at: 61, in: cues)?.text, "Third line")
    }

    func testTimestampFormats() {
        XCTAssertEqual(WebVTT.seconds(from: "00:00:01.500"), 1.5)
        XCTAssertEqual(WebVTT.seconds(from: "01:02.250"), 62.25)
        XCTAssertEqual(WebVTT.seconds(from: "01:00:00.000"), 3600)
        // SRT-style commas turn up in the wild.
        XCTAssertEqual(WebVTT.seconds(from: "00:00:02,000"), 2)
        XCTAssertNil(WebVTT.seconds(from: "nonsense"))
    }

    func testEmptyInputYieldsNoCues() {
        XCTAssertTrue(WebVTT.parse("").isEmpty)
        XCTAssertTrue(WebVTT.parse("WEBVTT\n\n").isEmpty)
    }

    // MARK: - Track selection

    private let tracks = [
        SubtitleTrack(lang: "en", source: .auto, url: "/media/subtitles/x/en.vtt"),
        SubtitleTrack(lang: "de", source: .user, url: "/media/subtitles/x/de.vtt")
    ]

    func testPickPrefersAnArchivedTrackInThePreferredLanguage() {
        XCTAssertEqual(SubtitleLoader.pick(from: tracks, preferred: "de")?.lang, "de")
    }

    func testPickFallsBackToAnAutoTrackInThePreferredLanguage() {
        XCTAssertEqual(SubtitleLoader.pick(from: tracks, preferred: "en")?.source, .auto)
    }

    /// English is the documented default when the preferred language has no track.
    func testPickFallsBackToEnglish() {
        XCTAssertEqual(SubtitleLoader.pick(from: tracks, preferred: "fr")?.lang, "en")
    }

    func testPickHonoursOff() {
        XCTAssertNil(SubtitleLoader.pick(from: tracks, preferred: Prefs.subtitlesOff))
    }

    func testPickOnAVideoWithNoTracks() {
        XCTAssertNil(SubtitleLoader.pick(from: [], preferred: "en"))
    }
}
