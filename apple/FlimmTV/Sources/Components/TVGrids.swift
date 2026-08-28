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

    @Environment(AppModel.self) private var app

    /// Set right after "Not interested" drops a card from a feed — what the
    /// undo banner acts on. A feed is the only context where dismissing
    /// removes the card at all; see ``handleDismissChange(_:)``.
    @State private var pendingUndo: PendingDismiss?

    private struct PendingDismiss: Identifiable {
        let video: VideoSummary
        let index: Int
        var id: String { video.id }
    }

    private var isFeedContext: Bool {
        if case .feed = context.source { return true }
        return false
    }

    var body: some View {
        LazyVGrid(columns: TVGrids.videos, alignment: .leading, spacing: TVMetrics.gridSpacing) {
            ForEach(pager.items) { video in
                TVVideoCard(video: video, context: context, showChannel: showChannel, onDismissChange: handleDismissChange)
                    .task { await pager.loadMoreIfNeeded(after: video) }
            }
        }
        if pager.isLoadingMore {
            ProgressView()
                .frame(maxWidth: .infinity)
                .padding(.vertical, 24)
        }
        if let pendingUndo {
            TVDismissUndoBanner(title: pendingUndo.video.title) {
                Task { await undoDismiss(pendingUndo) }
            }
        }
    }

    // MARK: - Dismiss / undo

    private func handleDismissChange(_ updated: VideoSummary) {
        if isFeedContext, updated.dismissed, let index = pager.items.firstIndex(where: { $0.id == updated.id }) {
            pendingUndo = PendingDismiss(video: updated, index: index)
            withAnimation { pager.remove(id: updated.id) }
        } else {
            // Channel, playlist, search, history: the video stays in view,
            // now carrying `dismissed: true` and its own "Add back" entry.
            pager.replace(updated)
        }
        // What every other cached list contains just changed — see
        // ``AppModel/videoListStateChanged()``.
        Task { await app.videoListStateChanged() }
    }

    private func undoDismiss(_ pending: PendingDismiss) async {
        if pendingUndo?.id == pending.id { pendingUndo = nil }
        guard let restored = await toggleDismissed(pending.video, client: app.client) else { return }
        withAnimation { pager.reinsert(restored, at: pending.index) }
        await app.videoListStateChanged()
    }
}

/// "Not interested: <title> — Undo", sized for the sofa. Anchored under the
/// grid it belongs to rather than floating over content, since there is no
/// safe-area inset worth fighting for on a fixed 10-foot layout.
struct TVDismissUndoBanner: View {
    let title: String
    let undo: () -> Void

    var body: some View {
        HStack(spacing: 24) {
            Text("Not interested: “\(title)”")
                .font(.title3)
                .lineLimit(1)
            Spacer(minLength: 20)
            Button("Undo", action: undo)
        }
        .padding(.horizontal, 30)
        .padding(.vertical, 18)
        .background(Palette.raised, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .padding(.top, 12)
    }
}
