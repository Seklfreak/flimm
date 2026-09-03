import Foundation
import Testing

@testable import FlimmKit

/// The Playlists screen leads with the pins and lists everything else below.
/// The list the server returns holds the pinned ones too, so the screen has to
/// take them out — otherwise a pinned playlist reads as two playlists.
@Suite struct PinnedPlaylistTests {
    private func playlist(_ id: String, pinned: Bool = false) -> PlaylistSummary {
        PlaylistSummary(id: id, name: id.capitalized, pinned: pinned)
    }

    @Test func dropsThePlaylistsThePinnedSectionAlreadyShows() {
        let all = [playlist("music", pinned: true), playlist("talks"), playlist("live")]
        let rest = all.excludingPinned([playlist("music", pinned: true)])
        #expect(rest.map(\.id) == ["talks", "live"])
    }

    /// Matching is by id: the pinned copy is fetched from its own endpoint, so
    /// its counts can differ from the one in the paged list.
    @Test func matchesOnIdRatherThanValue() {
        let listed = PlaylistSummary(id: "music", name: "Music", videoCount: 62, pinned: true)
        let pinned = PlaylistSummary(id: "music", name: "Music", videoCount: 61, pinned: true)
        #expect([listed].excludingPinned([pinned]).isEmpty)
    }

    @Test func keepsEverythingWhenNothingIsPinned() {
        let all = [playlist("talks"), playlist("live")]
        #expect(all.excludingPinned([]).map(\.id) == ["talks", "live"])
    }
}
