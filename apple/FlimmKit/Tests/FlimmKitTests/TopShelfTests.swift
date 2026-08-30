import XCTest
@testable import FlimmKit

final class TopShelfTests: XCTestCase {
    /// The store is exercised against a real directory rather than the App
    /// Group, which a unit test has no entitlement for.
    private var group: String { "test.group.\(name.hashValue)" }

    func testLinksRoundTrip() {
        let url = TopShelfLink.play("yt-id_123")
        XCTAssertEqual(url.absoluteString, "dev.winktech.flimm.tv://play/yt-id_123")
        XCTAssertEqual(TopShelfLink.videoID(from: url), "yt-id_123")
    }

    /// The app is opened with all sorts of URLs; only the shelf's own mean
    /// "play this".
    func testOtherURLsAreNotVideos() {
        for raw in [
            "dev.winktech.flimm.tv://play",
            "dev.winktech.flimm.tv://auth/callback",
            "https://flimm.example.com/watch/abc",
            "dev.winktech.flimm://play/abc"
        ] {
            XCTAssertNil(TopShelfLink.videoID(from: URL(string: raw)!), raw)
        }
    }

    func testSnapshotEncodesAndDecodes() throws {
        let snapshot = TopShelfSnapshot(
            feedName: "Making",
            entries: [
                TopShelfEntry(videoID: "v1", title: "A", channel: "Chan",
                              imageURL: "https://flimm.example.com/media/thumb/video/v1?media_token=t",
                              progress: 0.4, duration: 600),
                TopShelfEntry(videoID: "v2", title: "B", channel: "Chan", imageURL: nil,
                              progress: 0, duration: 90)
            ],
            updatedAt: Date(timeIntervalSince1970: 1_700_000_000)
        )
        let data = try FlimmCoding.encoder.encode(snapshot)
        let back = try FlimmCoding.decoder.decode(TopShelfSnapshot.self, from: data)
        XCTAssertEqual(back, snapshot)
    }

    /// An entry with no artwork URL still belongs on the shelf; it just has no
    /// picture. tvOS draws its own placeholder.
    func testAnEntryCanHaveNoImage() {
        let entry = TopShelfEntry(videoID: "v1", title: "A", channel: "C", imageURL: nil,
                                  progress: 0, duration: 10)
        XCTAssertNil(entry.imageURL)
    }

    /// Nothing written yet — nobody has signed in, or this build has no App
    /// Group at all — has to read as an empty shelf rather than an error.
    func testAnUnwrittenShelfReadsAsNothing() {
        XCTAssertNil(TopShelfStore.read(group: "group.never.written.\(UUID().uuidString)"))
    }

    /// The snapshot round-trips through the group's defaults, which is how the
    /// extension sees it — and on tvOS the only shared storage there is: the
    /// group's *container* is not writable there, which is what "You don't
    /// have permission to save the file" on a real Apple TV turned out to
    /// mean.
    func testSnapshotSurvivesTheStore() throws {
        let group = group
        addTeardownBlock { TopShelfStore.clear(group: group) }
        XCTAssertNil(TopShelfStore.read(group: group), "the suite should start empty")
        let entry = TopShelfEntry(
            videoID: "v1", title: "A", channel: "C",
            imageURL: "https://flimm.example.com/media/thumb/video/v1?media_token=t",
            progress: 0.25, duration: 300
        )
        let snapshot = TopShelfSnapshot(feedName: "Making", entries: [entry], updatedAt: Date())
        try TopShelfStore.write(snapshot, group: group)
        let back = try XCTUnwrap(TopShelfStore.read(group: group))
        XCTAssertEqual(back.entries.first?.videoID, "v1")
        XCTAssertEqual(back.entries.first?.progress, 0.25)
        XCTAssertEqual(back.feedName, "Making")

        XCTAssertEqual(back.entries.first?.imageURL?.contains("media_token"), true)
    }
}
