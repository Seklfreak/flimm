import XCTest
@testable import FlimmKit

final class ChapterMathTests: XCTestCase {
    private let chapters = [
        Chapter(start: 0, end: 60, title: "Intro"),
        Chapter(start: 60, end: 180, title: "Middle"),
        Chapter(start: 180, end: 240, title: "Outro")
    ]

    func testIndexOfTheChapterContainingATime() {
        XCTAssertEqual(ChapterMath.index(of: 0, in: chapters), 0)
        XCTAssertEqual(ChapterMath.index(of: 59.9, in: chapters), 0)
        XCTAssertEqual(ChapterMath.index(of: 60, in: chapters), 1)
        XCTAssertEqual(ChapterMath.index(of: 10_000, in: chapters), 2)
    }

    /// Every chapter affordance has to vanish cleanly on a video with none.
    func testNoChaptersMeansNoIndexAndNoMarkers() {
        XCTAssertEqual(ChapterMath.index(of: 12, in: []), -1)
        XCTAssertTrue(ChapterMath.markerFractions([], duration: 240).isEmpty)
        XCTAssertTrue(ChapterMath.markerFractions(Array(chapters.prefix(1)), duration: 240).isEmpty)
    }

    /// The first chapter always starts at 0, so its tick would sit on the very
    /// edge of the bar and is skipped.
    func testMarkersSkipTheFirstChapter() {
        let marks = ChapterMath.markerFractions(chapters, duration: 240)
        XCTAssertEqual(marks.count, 2)
        XCTAssertEqual(marks[0], 0.25, accuracy: 0.0001)
        XCTAssertEqual(marks[1], 0.75, accuracy: 0.0001)
    }

    func testMarkersNeedADuration() {
        XCTAssertTrue(ChapterMath.markerFractions(chapters, duration: 0).isEmpty)
    }

    /// `]` on the keyboard and the chapter-forward transport control.
    func testNextChapterStart() {
        XCTAssertEqual(ChapterMath.nextStart(after: 0, in: chapters), 60)
        XCTAssertEqual(ChapterMath.nextStart(after: 59, in: chapters), 60)
        XCTAssertEqual(ChapterMath.nextStart(after: 60, in: chapters), 180)
        XCTAssertNil(ChapterMath.nextStart(after: 200, in: chapters))
        XCTAssertNil(ChapterMath.nextStart(after: 0, in: []))
    }

    /// `[` restarts the current chapter once past the threshold, and steps back
    /// a chapter while still near its start.
    func testPreviousChapterStart() {
        XCTAssertEqual(ChapterMath.previousStart(before: 100, in: chapters), 60)
        XCTAssertEqual(ChapterMath.previousStart(before: 61, in: chapters), 0)
        XCTAssertNil(ChapterMath.previousStart(before: 1, in: chapters))
        XCTAssertNil(ChapterMath.previousStart(before: 12, in: []))
    }
}

final class SponsorMuteTrackerTests: XCTestCase {
    private let segments = [SponsorSegment(category: "sponsor", actionType: .mute, start: 10, end: 20)]
    private let prefs = Prefs()

    func testMutesForTheSegmentAndRestoresAfterIt() {
        var tracker = SponsorMuteTracker()
        XCTAssertNil(tracker.mute(at: 5, in: segments, prefs: prefs, isMuted: false))
        XCTAssertEqual(tracker.mute(at: 12, in: segments, prefs: prefs, isMuted: false), true)
        // Already ours: nothing to change while it runs.
        XCTAssertNil(tracker.mute(at: 15, in: segments, prefs: prefs, isMuted: true))
        XCTAssertEqual(tracker.mute(at: 25, in: segments, prefs: prefs, isMuted: true), false)
    }

    func testAViewerWhoWasAlreadyMutedStaysMuted() {
        var tracker = SponsorMuteTracker()
        XCTAssertEqual(tracker.mute(at: 12, in: segments, prefs: prefs, isMuted: true), true)
        XCTAssertEqual(tracker.mute(at: 25, in: segments, prefs: prefs, isMuted: true), true)
    }

    func testTheSegmentIsIgnoredWhenTheSkipPreferenceIsOff() {
        var tracker = SponsorMuteTracker()
        XCTAssertNil(tracker.mute(at: 12, in: segments, prefs: Prefs(skipSponsors: false), isMuted: false))
    }
}

final class SponsorRulesTests: XCTestCase {
    private let segments = [
        SponsorSegment(category: "sponsor", start: 10, end: 30),
        SponsorSegment(category: "intro", start: 40, end: 50),
        SponsorSegment(category: "selfpromo", start: 100, end: 110)
    ]

    /// This suite's viewer, with the usual defaults.
    private let prefs = Prefs()

    /// A viewer who set these categories to something of their own.
    private func viewer(_ actions: [String: SponsorSetting]) -> Prefs {
        Prefs(sponsorActions: actions)
    }

    func testOnlyCategoriesSetToSkipAreSkipped() {
        XCTAssertEqual(SponsorRules.segmentToSkip(at: 15, in: segments, prefs: prefs)?.category, "sponsor")
        XCTAssertEqual(SponsorRules.segmentToSkip(at: 105, in: segments, prefs: prefs)?.category, "selfpromo")
        // An intro defaults to "ask": offered on the way past, never taken.
        XCTAssertNil(SponsorRules.segmentToSkip(at: 45, in: segments, prefs: prefs))
        XCTAssertNil(SponsorRules.segmentToSkip(at: 5, in: segments, prefs: prefs))
    }

    /// A margin at the end stops a skip landing just inside the boundary and
    /// re-triggering forever.
    func testTheEndMarginPreventsASkipLoop() {
        XCTAssertNil(SponsorRules.segmentToSkip(at: 29.8, in: segments, prefs: prefs))
    }

    func testZeroLengthSegmentsAreIgnored() {
        let degenerate = [SponsorSegment(category: "sponsor", start: 10, end: 10)]
        XCTAssertNil(SponsorRules.segmentToSkip(at: 10, in: degenerate, prefs: prefs))
        XCTAssertTrue(SponsorRules.ranges(degenerate, duration: 100).isEmpty)
    }

    func testRangesAreFractionsOfTheBar() {
        let ranges = SponsorRules.ranges(segments, duration: 200)
        XCTAssertEqual(ranges.count, 3)
        XCTAssertEqual(ranges[0].start, 0.05, accuracy: 0.0001)
        XCTAssertEqual(ranges[0].width, 0.1, accuracy: 0.0001)
    }

    /// A segment reaching past the end of the video must not draw past the bar.
    func testRangesAreClampedToTheBar() {
        let overrun = [SponsorSegment(category: "sponsor", start: 150, end: 5_000)]
        let ranges = SponsorRules.ranges(overrun, duration: 200)
        XCTAssertEqual(ranges[0].start + ranges[0].width, 1, accuracy: 0.0001)
    }

    func testAMissingActionTypeIsASkip() throws {
        let json = Data(#"{ "category": "sponsor", "start": 1, "end": 2 }"#.utf8)
        let segment = try FlimmCoding.decoder.decode(SponsorSegment.self, from: json)
        XCTAssertEqual(segment.actionType, .skip)
    }

    func testOnlySkipSegmentsAreSkipped() {
        let mixed = [
            SponsorSegment(category: "sponsor", actionType: .mute, start: 10, end: 30),
            SponsorSegment(category: "selfpromo", actionType: .other, start: 40, end: 50)
        ]
        // A mute segment keeps playing, and an action this build does not
        // understand is inert rather than guessed at.
        XCTAssertNil(SponsorRules.segmentToSkip(at: 15, in: mixed, prefs: prefs))
        XCTAssertNil(SponsorRules.segmentToSkip(at: 45, in: mixed, prefs: prefs))
    }

    /// A category is skipped because the viewer said so, not because it is a
    /// sponsor: the same segment is jumped, offered or ignored depending on
    /// what they set.
    func testACategoryActsAsTheViewerSetIt() {
        let intro = [SponsorSegment(category: "intro", actionType: .skip, start: 0, end: 30)]
        XCTAssertNil(SponsorRules.segmentToSkip(at: 5, in: intro, prefs: prefs))
        XCTAssertEqual(SponsorRules.segmentToOffer(at: 5, in: intro, prefs: prefs)?.category, "intro")

        XCTAssertEqual(SponsorRules.segmentToSkip(at: 5, in: intro, prefs: viewer(["intro": .skip]))?.category, "intro")
        XCTAssertNil(SponsorRules.segmentToOffer(at: 5, in: intro, prefs: viewer(["intro": .skip])))

        XCTAssertNil(SponsorRules.segmentToSkip(at: 5, in: intro, prefs: viewer(["intro": .off])))
        XCTAssertNil(SponsorRules.segmentToOffer(at: 5, in: intro, prefs: viewer(["intro": .off])))
        // A category this build has never heard of does nothing at all.
        XCTAssertNil(SponsorRules.segmentToSkip(at: 5, in: intro, prefs: viewer([:])))
    }

    /// A button that appears for the last second of a segment is a button
    /// nobody can press, so the offer stops before the skip would.
    func testAnOfferStopsBeforeTheSegmentEnds() {
        let intro = [SponsorSegment(category: "intro", actionType: .skip, start: 0, end: 30)]
        XCTAssertNotNil(SponsorRules.segmentToOffer(at: 28.4, in: intro, prefs: prefs))
        XCTAssertNil(SponsorRules.segmentToOffer(at: 28.6, in: intro, prefs: prefs))
    }

    func testMuteSegmentsRunToTheirVeryEnd() {
        let mute = [SponsorSegment(category: "sponsor", actionType: .mute, start: 10, end: 30)]
        XCTAssertEqual(SponsorRules.segmentToMute(at: 10, in: mute, prefs: prefs)?.category, "sponsor")
        XCTAssertNotNil(SponsorRules.segmentToMute(at: 29.9, in: mute, prefs: prefs))
        XCTAssertNil(SponsorRules.segmentToMute(at: 30, in: mute, prefs: prefs))
        // A skip segment is not muted on the way past.
        XCTAssertNil(SponsorRules.segmentToMute(at: 15, in: segments, prefs: prefs))
        // "Ask" still mutes: the viewer asked to be offered the skip, not to
        // hear the sponsor read while they decide. Only "off" leaves it.
        XCTAssertNotNil(SponsorRules.segmentToMute(at: 15, in: mute, prefs: viewer(["sponsor": .ask])))
        XCTAssertNil(SponsorRules.segmentToMute(at: 15, in: mute, prefs: viewer(["sponsor": .off])))
    }

    func testPointsOfInterestAndWholeVideoLabelsAreNotTinted() {
        let extras = [
            SponsorSegment(category: "poi_highlight", actionType: .poi, start: 50, end: 50),
            SponsorSegment(category: "sponsor", actionType: .full, start: 0, end: 200),
            SponsorSegment(category: "sponsor", actionType: .skip, start: 0, end: 100)
        ]
        let ranges = SponsorRules.ranges(extras, duration: 200)
        XCTAssertEqual(ranges.count, 1)
        XCTAssertEqual(ranges[0].width, 0.5, accuracy: 0.0001)
    }

    func testTheHighlightIsFoundAndOfferedOnlyAhead() {
        let withHighlight = [
            SponsorSegment(category: "sponsor", actionType: .skip, start: 0, end: 30),
            SponsorSegment(category: "poi_highlight", actionType: .poi, start: 90, end: 90)
        ]
        XCTAssertEqual(SponsorRules.highlight(in: withHighlight)?.start, 90)
        XCTAssertNil(SponsorRules.highlight(in: segments))
        XCTAssertEqual(SponsorRules.highlightToOffer(at: 0, in: withHighlight)?.start, 90)
        // Inside the lead the viewer is already there, and past it there is
        // nothing to offer.
        XCTAssertNil(SponsorRules.highlightToOffer(at: 89.5, in: withHighlight))
        XCTAssertNil(SponsorRules.highlightToOffer(at: 120, in: withHighlight))
    }

    func testTheEarliestHighlightWins() {
        let several = [
            SponsorSegment(category: "poi_highlight", actionType: .poi, start: 120, end: 120),
            SponsorSegment(category: "poi_highlight", actionType: .poi, start: 40, end: 40)
        ]
        XCTAssertEqual(SponsorRules.highlight(in: several)?.start, 40)
    }

    func testPointsAreMarkersNotBands() {
        let withHighlight = [
            SponsorSegment(category: "sponsor", actionType: .skip, start: 0, end: 30),
            SponsorSegment(category: "poi_highlight", actionType: .poi, start: 90, end: 90)
        ]
        XCTAssertEqual(SponsorRules.pointFractions(withHighlight, duration: 180), [0.5])
        XCTAssertEqual(SponsorRules.ranges(withHighlight, duration: 180).count, 1)
    }

    func testLabelsFallBackToAReadableCategory() {
        XCTAssertEqual(SponsorRules.label("selfpromo"), "Self-promo")
        XCTAssertEqual(SponsorRules.label("some_new_category"), "some new category")
    }
}
