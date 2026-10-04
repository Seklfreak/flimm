import FlimmKit
import SwiftUI

/// Finding one video in a long playlist, on its own screen.
///
/// tvOS draws `.searchable` as a keyboard across the top of the screen, which
/// belongs on a screen whose job is searching — not on every playlist page.
/// So the playlist header offers *Filter* and this is where it leads, laid
/// out like the Search tab. The matching rule is FlimmKit's
/// ``PlaylistFilter``, the same one the web and the phone use; playing a match
/// still plays it in the playlist, so up next carries on in playlist order.
struct TVPlaylistFilterView: View {
    @Binding var playlist: Playlist?
    let context: PlaybackContext
    let canMarkSeen: Bool
    let onVideoChange: (VideoSummary) -> Void

    @State private var query: String = {
        #if DEBUG
        // See TVPlaylistDetailView: a simulator has no remote to type with.
        return ProcessInfo.processInfo.environment["FLIMM_PLAYLIST_FILTER"] ?? ""
        #else
        return ""
        #endif
    }()

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                TVScreenTitle(title: playlist?.name ?? "Playlist")
                    .padding(.top, 20)
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .searchable(text: $query, prompt: "Filter playlist")
    }

    @ViewBuilder
    private var content: some View {
        let items = PlaylistFilter.filter(playlist?.items ?? [], query: query)
        if items.isEmpty {
            TVEmptyState(icon: "magnifyingglass", title: "Nothing here matches", message: "Matches titles and channel names.")
        } else {
            LazyVGrid(columns: TVGrids.videos, alignment: .leading, spacing: TVMetrics.gridSpacing) {
                ForEach(items) { item in
                    TVVideoCard(video: item.video, context: context, canMarkSeen: canMarkSeen, onVideoChange: onVideoChange)
                }
            }
        }
    }
}
