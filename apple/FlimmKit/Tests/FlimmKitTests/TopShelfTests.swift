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

    /// The shelf's "nothing here yet" card opens the app and plays nothing.
    func testTheOpenLinkIsNotAVideo() {
        XCTAssertEqual(TopShelfLink.open.absoluteString, "dev.winktech.flimm.tv://open")
        XCTAssertNil(TopShelfLink.videoID(from: TopShelfLink.open))
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
                TopShelfEntry(videoID: "v1", title: "A", channel: "Chan", imageName: "v1-abc.jpg",
                              progress: 0.4, duration: 600),
                TopShelfEntry(videoID: "v2", title: "B", channel: "Chan", imageName: nil,
                              progress: 0, duration: 90)
            ],
            updatedAt: Date(timeIntervalSince1970: 1_700_000_000)
        )
        let data = try FlimmCoding.encoder.encode(snapshot)
        let back = try FlimmCoding.decoder.decode(TopShelfSnapshot.self, from: data)
        XCTAssertEqual(back, snapshot)
    }

    /// An entry whose image never downloaded still belongs on the shelf; it
    /// just has no picture.
    func testAnEntryWithoutAnImageHasNoImageURL() {
        let entry = TopShelfEntry(videoID: "v1", title: "A", channel: "C", imageName: nil,
                                  progress: 0, duration: 10)
        XCTAssertNil(TopShelfStore.imageURL(for: entry, group: group))
    }

    /// Nothing written yet — nobody has signed in, or this build has no App
    /// Group at all — has to read as an empty shelf rather than an error.
    func testAnUnwrittenShelfReadsAsNothing() {
        XCTAssertNil(TopShelfStore.read(group: "group.never.written.\(UUID().uuidString)"))
    }

    /// The snapshot survives a round trip through a real file, which is the
    /// only way the extension ever sees one.
    func testSnapshotSurvivesTheFile() throws {
        guard let dir = TopShelfStore.directory(for: group) else {
            throw XCTSkip("no group container on this platform")
        }
        let group = group
        addTeardownBlock { TopShelfStore.clear(group: group) }
        let entry = TopShelfEntry(videoID: "v1", title: "A", channel: "C", imageName: "v1.jpg",
                                  progress: 0.25, duration: 300)
        let snapshot = TopShelfSnapshot(feedName: "Making", entries: [entry], updatedAt: Date())
        try TopShelfStore.write(snapshot, group: group)
        let back = try XCTUnwrap(TopShelfStore.read(group: group))
        XCTAssertEqual(back.entries.first?.videoID, "v1")
        XCTAssertEqual(back.entries.first?.progress, 0.25)
        XCTAssertEqual(back.feedName, "Making")

        // An image the snapshot does not name is swept up; one it does is kept.
        try Data("x".utf8).write(to: dir.appendingPathComponent("v1.jpg"))
        try Data("x".utf8).write(to: dir.appendingPathComponent("stale.jpg"))
        TopShelfStore.pruneImages(keeping: snapshot, group: group)
        XCTAssertNotNil(TopShelfStore.imageURL(for: entry, group: group))
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("stale.jpg").path))
        XCTAssertNotNil(TopShelfStore.read(group: group), "pruning must not take the snapshot with it")
    }
}
