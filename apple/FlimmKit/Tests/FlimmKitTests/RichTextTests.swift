import XCTest
@testable import FlimmKit

/// Mirrors the web client's `richText.test.ts`, case for case: the two must
/// link the same things.
final class RichTextTests: XCTestCase {
    private func url(_ s: String) -> URL { URL(string: s)! }

    func testPlainTextStaysAsItIs() {
        XCTAssertEqual(RichText.segments("one\n\ntwo"), [.text("one\n\ntwo")])
    }

    func testLinksAURLAndLeavesTheSentencesPunctuationOutside() {
        XCTAssertEqual(RichText.segments("see https://example.com/a?b=1."), [
            .text("see "),
            .link("https://example.com/a?b=1", url("https://example.com/a?b=1")),
            .text("."),
        ])
    }

    func testAnUnmatchedClosingBracketBelongsToTheSentence() {
        XCTAssertEqual(RichText.segments("(at https://example.com/x)."), [
            .text("(at "),
            .link("https://example.com/x", url("https://example.com/x")),
            .text(")."),
        ])
        // A bracket the URL itself opened stays: Wikipedia does this.
        XCTAssertEqual(RichText.segments("https://en.wikipedia.org/wiki/Jig_(tool)"), [
            .link("https://en.wikipedia.org/wiki/Jig_(tool)", url("https://en.wikipedia.org/wiki/Jig_(tool)")),
        ])
    }

    func testABareWWWHostGetsAScheme() {
        XCTAssertEqual(RichText.segments("www.example.com rocks"), [
            .link("www.example.com", url("https://www.example.com")),
            .text(" rocks"),
        ])
    }

    func testTimestampsSeekWithAndWithoutAnHour() {
        XCTAssertEqual(RichText.segments("0:00 Intro\n1:02:03 End"), [
            .time("0:00", 0),
            .text(" Intro\n"),
            .time("1:02:03", 3723),
            .text(" End"),
        ])
    }

    func testDoesNotSeekPastTheEndOfTheVideo() {
        XCTAssertEqual(RichText.segments("at 2:30 and 0:45", duration: 90), [
            .text("at 2:30 and "),
            .time("0:45", 45),
        ])
    }

    func testRejectsWhatOnlyLooksLikeATime() {
        XCTAssertEqual(
            RichText.segments("1:75 v2:30 John 3:16b 10:30:45:12"),
            [.text("1:75 v2:30 John 3:16b 10:30:45:12")]
        )
    }

    func testALinkKeepsATimestampThatSitsInsideIt() {
        XCTAssertEqual(RichText.segments("https://example.com/watch/1:30 and 1:30"), [
            .link("https://example.com/watch/1:30", url("https://example.com/watch/1:30")),
            .text(" and "),
            .time("1:30", 90),
        ])
    }

    func testASeekURLRoundTripsAndARealOneIsNotOne() {
        XCTAssertEqual(RichText.seekSeconds(RichText.seekURL(90)), 90)
        XCTAssertNil(RichText.seekSeconds(url("https://example.com/1:30")))
    }
}
