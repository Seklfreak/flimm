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
    }

    @ViewBuilder
    private var cards: some View {
        ForEach(pager.items) { video in
            VideoCard(video: video, context: context, showChannel: showChannel)
                .task { await pager.loadMoreIfNeeded(after: video) }
        }
    }
}
