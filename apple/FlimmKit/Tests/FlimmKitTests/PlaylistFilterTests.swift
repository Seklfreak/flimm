import Foundation
import Testing

@testable import FlimmKit

/// The same cases as the web's playlistFilter.test.ts: the two clients must
/// agree on what a filter matches.
@Suite struct PlaylistFilterTests {
    private func item(_ position: Int, _ title: String, _ channel: String) -> PlaylistItem {
        PlaylistItem(position: position, video: VideoSummary(
            id: "v\(position)",
            title: title,
            channel: VideoChannelRef(id: "UC\(position)", name: channel, thumbUrl: ""),
            thumbUrl: "",
            duration: 200
        ))
    }

    private var items: [PlaylistItem] {
        [item(0, "Winter", "Daughter"), item(1, "Halo", "Beyoncé"), item(2, "Winter Song", "Sara Bareilles")]
    }

    private func positions(_ query: String) -> [Int] {
        PlaylistFilter.filter(items, query: query).map(\.position)
    }

    @Test func emptyOrBlankQueryKeepsEverything() {
        #expect(positions("") == [0, 1, 2])
        #expect(positions("   ") == [0, 1, 2])
    }

    @Test func matchesTitleOrChannelIgnoringCase() {
        #expect(positions("WINTER") == [0, 2])
        #expect(positions("daughter") == [0])
    }

    @Test func needsEveryWordInAnyOrderAcrossTitleAndChannel() {
        #expect(positions("winter daughter") == [0])
        #expect(positions("daughter   winter") == [0])
        #expect(positions("winter halo").isEmpty)
    }

    @Test func ignoresAccentsOnEitherSide() {
        #expect(positions("beyonce") == [1])
        #expect(positions("béyoncé") == [1])
    }

    @Test func keepsPlaylistOrderAndPositions() {
        #expect(PlaylistFilter.filter(items, query: "song").first?.position == 2)
    }
}
