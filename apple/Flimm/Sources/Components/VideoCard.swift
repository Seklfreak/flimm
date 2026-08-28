import FlimmKit
import SwiftUI

/// The 16:9 thumbnail with its overlays: resume pill, seen check, duration and
/// the progress bar. `position > 0` on an unwatched video means "in progress" —
/// the same rule the API contract states, not a local heuristic.
struct VideoThumbnail: View {
    let video: VideoSummary
    var compact = false

    private var inProgress: Bool { !video.watched && video.position > 0 }

    var body: some View {
        MediaImage(path: video.thumbUrl)
            .aspectRatio(16 / 9, contentMode: .fill)
            .clipShape(RoundedRectangle(cornerRadius: compact ? 8 : 14, style: .continuous))
            .overlay(alignment: .topLeading) { topLeading }
            .overlay(alignment: .topTrailing) {
                // A dismissed video only ever reaches this thumbnail on a
                // channel, playlist, search or history card — a feed drops
                // it server-side, and ``VideoList``/``TVVideoGrid`` drop the
                // card locally the moment it happens there. This is the "say
                // so" half of putting one back; the "Add back to feeds"
                // context-menu entry is the other.
                if video.dismissed {
                    Text("Not in feeds")
                        .pillStyle()
                        .padding(compact ? 4 : 8)
                }
            }
            .overlay(alignment: .bottomTrailing) {
                Text(Fmt.duration(video.duration))
                    .pillStyle()
                    .padding(compact ? 4 : 8)
            }
            .overlay(alignment: .bottom) {
                if inProgress {
                    ProgressBar(value: video.progress)
                        .padding(.horizontal, compact ? 4 : 8)
                        .padding(.bottom, compact ? 4 : 8)
                }
            }
    }

    @ViewBuilder
    private var topLeading: some View {
        if video.watched {
            Image(systemName: "checkmark")
                .font(.system(size: compact ? 9 : 11, weight: .bold))
                .foregroundStyle(.white)
                .frame(width: compact ? 18 : 22, height: compact ? 18 : 22)
                .background(Palette.overlay, in: Circle())
                .padding(compact ? 4 : 8)
        } else if inProgress && !compact {
            Text("Resume · \(Fmt.duration(video.position))")
                .pillStyle()
                .padding(8)
        }
    }
}

struct ProgressBar: View {
    let value: Double

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.white.opacity(0.35))
                Capsule()
                    .fill(Palette.accent)
                    .frame(width: geo.size.width * min(max(value, 0), 1))
            }
        }
        .frame(height: 3)
    }
}

/// The grid/list card used by every video list.
struct VideoCard: View {
    let video: VideoSummary
    var context: PlaybackContext = .none
    var showChannel = true
    /// Called with the updated summary once a dismiss/undismiss round trip
    /// succeeds. See ``DismissMenuItem``.
    var onDismissChange: ((VideoSummary) -> Void)?

    @Environment(PlayerCoordinator.self) private var player

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            VideoThumbnail(video: video)
            VStack(alignment: .leading, spacing: 2) {
                Text(video.title)
                    .font(.system(size: 16, weight: .bold))
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                Text(meta)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .opacity(video.watched ? 0.55 : 1)
        .contentShape(Rectangle())
        .onTapGesture { player.play(video, context: context) }
        .contextMenu { DismissMenuItem(video: video, onChange: onDismissChange) }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(video.title)
        .accessibilityAddTraits(.isButton)
    }

    private var meta: String {
        var parts: [String] = []
        if showChannel { parts.append(video.channel.name) }
        if video.watched {
            parts.append(Fmt.seenLabel(video.lastPlayedAt))
        } else {
            parts.append(Fmt.ccLabel(langs: video.subtitleLangs, hasAuto: video.hasAutoSubtitles))
            parts.append(Fmt.relativeDay(video.published))
        }
        return parts.joined(separator: " · ")
    }
}

/// The compact horizontal row — up next, history, search results.
struct VideoRow: View {
    let video: VideoSummary
    var context: PlaybackContext = .none
    var subtitle: String?
    /// Called with the updated summary once a dismiss/undismiss round trip
    /// succeeds. `VideoRow` is never used inside a feed, so every caller just
    /// patches the row in place — see ``DismissMenuItem``.
    var onDismissChange: ((VideoSummary) -> Void)?

    @Environment(PlayerCoordinator.self) private var player

    var body: some View {
        Button {
            player.play(video, context: context)
        } label: {
            HStack(alignment: .top, spacing: 12) {
                VideoThumbnail(video: video, compact: true)
                    .frame(width: 132)
                VStack(alignment: .leading, spacing: 3) {
                    Text(video.title)
                        .font(.subheadline.weight(.bold))
                        .lineLimit(2)
                        .multilineTextAlignment(.leading)
                    Text(subtitle ?? defaultSubtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                        .multilineTextAlignment(.leading)
                }
                Spacer(minLength: 0)
            }
        }
        .buttonStyle(.plain)
        .opacity(video.watched ? 0.6 : 1)
        .contextMenu { DismissMenuItem(video: video, onChange: onDismissChange) }
    }

    private var defaultSubtitle: String {
        if !video.watched && video.position > 0 {
            return "\(video.channel.name) · \(Fmt.duration(video.position)) / \(Fmt.duration(video.duration))"
        }
        return "\(video.channel.name) · \(Fmt.duration(video.duration))"
    }
}

/// The video list every screen uses, with the "load more on the last row"
/// sentinel wired in.
///
/// One column on a phone, a grid where the window is wide enough — the columns
/// come from the container's width, so an iPad in Split View simply gets fewer
/// of them (see ``Grids``).
struct VideoList: View {
    let pager: Pager<VideoSummary>
    var context: PlaybackContext = .none
    var showChannel = true

    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
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
        VStack(spacing: 0) {
            if horizontalSizeClass == .regular {
                LazyVGrid(columns: Grids.videos, alignment: .leading, spacing: Grids.spacing) {
                    cards
                }
            } else {
                LazyVStack(alignment: .leading, spacing: 24) {
                    cards
                }
            }
            if pager.isLoadingMore {
                ProgressView()
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 12)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .safeAreaInset(edge: .bottom) {
            if let pendingUndo {
                DismissUndoBanner(title: pendingUndo.video.title) {
                    Task { await undoDismiss(pendingUndo) }
                }
            }
        }
    }

    @ViewBuilder
    private var cards: some View {
        ForEach(pager.items) { video in
            VideoCard(video: video, context: context, showChannel: showChannel, onDismissChange: handleDismissChange)
                .task { await pager.loadMoreIfNeeded(after: video) }
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

/// "Not interested: <title> — Undo". Anchored to the list rather than the
/// card that triggered it, so the viewer can reach it without having
/// scrolled — dismissing is meant to be reachable without navigating
/// anywhere else.
struct DismissUndoBanner: View {
    let title: String
    let undo: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Text("Not interested: “\(title)”")
                .font(.footnote)
                .lineLimit(1)
            Spacer(minLength: 8)
            Button("Undo", action: undo)
                .font(.footnote.weight(.semibold))
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .padding(.horizontal, 16)
        .padding(.bottom, 8)
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }
}
