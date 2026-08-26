import FlimmKit
import SwiftUI

/// Column definitions for the TV grids.
///
/// `.adaptive` rather than a fixed count so a 1080p and a 4K display both get
/// sensible rows, and so a card's minimum width — not a magic number — decides
/// how many fit inside the overscan-safe margins.
enum TVGrids {
    static let videos = [
        GridItem(.adaptive(minimum: 360, maximum: 520), spacing: TVMetrics.gridSpacing, alignment: .top)
    ]

    /// Wider tiles for playlists and channels, which carry more text.
    static let tiles = [
        GridItem(.adaptive(minimum: 400, maximum: 560), spacing: TVMetrics.gridSpacing, alignment: .top)
    ]
}

/// A grid of video cards with the "load the next page as the end comes into
/// view" sentinel wired in.
///
/// The sentinel is five rows from the end rather than on the last one: the
/// focus engine moves a whole row at a time and a remote's swipe covers
/// several, so waiting for the final card would show an empty tail.
struct TVVideoGrid: View {
    let pager: Pager<VideoSummary>
    var context: PlaybackContext = .none
    var showChannel = true

    var body: some View {
        LazyVGrid(columns: TVGrids.videos, alignment: .leading, spacing: TVMetrics.gridSpacing) {
            ForEach(pager.items) { video in
                TVVideoCard(video: video, context: context, showChannel: showChannel)
                    .task { await pager.loadMoreIfNeeded(after: video) }
            }
        }
        if pager.isLoadingMore {
            ProgressView()
                .frame(maxWidth: .infinity)
                .padding(.vertical, 24)
        }
    }
}
