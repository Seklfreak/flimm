import FlimmKit
import SwiftUI

/// The 16:9 thumbnail with its overlays: resume pill, seen check, duration and
/// the progress bar. `position > 0` on an unwatched video means "in progress" —
/// the same rule the API contract states, not a local heuristic.
struct TVVideoThumbnail: View {
    let video: VideoSummary

    private var inProgress: Bool { !video.watched && video.position > 0 }

    var body: some View {
        MediaImage(path: video.thumbUrl)
            .aspectRatio(16 / 9, contentMode: .fill)
            .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(alignment: .topLeading) { topLeading }
            .overlay(alignment: .topTrailing) {
                // A dismissed video only reaches this thumbnail on a
                // channel, playlist, search or history card — a feed drops
                // it server-side, and ``TVVideoGrid`` drops the card locally
                // the moment it happens there. This is the "say so" half of
                // putting one back; "Add back to feeds" in the card's own
                // context menu is the other.
                if video.dismissed {
                    Text("Not in feeds").tvPillStyle().padding(10)
                }
            }
            .overlay(alignment: .bottomTrailing) {
                HStack(spacing: 6) {
                    // Subtitles used to be a third part of the meta line,
                    // where it pushed the date out of a one-line label ("The
                    // Workshop · CC EN · 5 d…"). A badge says the same thing
                    // and costs the line nothing.
                    if !video.subtitleLangs.isEmpty {
                        Text("CC").tvPillStyle()
                    }
                    Text(Fmt.duration(video.duration)).tvPillStyle()
                }
                .padding(10)
            }
            .overlay(alignment: .bottom) {
                if inProgress {
                    TVProgressBar(value: video.progress)
                        .padding(.horizontal, 10)
                        .padding(.bottom, 10)
                }
            }
    }

    @ViewBuilder
    private var topLeading: some View {
        if video.watched {
            Image(systemName: "checkmark")
                .font(.system(size: 15, weight: .bold))
                .foregroundStyle(.white)
                .frame(width: 30, height: 30)
                .background(Palette.overlay, in: Circle())
                .padding(10)
        } else if inProgress {
            Text("Resume · \(Fmt.duration(video.position))")
                .tvPillStyle()
                .padding(10)
        }
    }
}

struct TVProgressBar: View {
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
        .frame(height: 6)
    }
}

extension View {
    /// The dark pill on a thumbnail, at TV reading distance.
    func tvPillStyle() -> some View {
        font(.caption.weight(.bold))
            .foregroundStyle(.white)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(Palette.overlay, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

/// The grid card.
///
/// `.card` is tvOS's own focus treatment — the lift, the shadow and the
/// parallax tilt as the remote's touch surface is moved. Rebuilding it by hand
/// with a scale effect gets the size right and the feel wrong, so the built-in
/// style does the focus work and only the title emphasis is ours.
struct TVVideoCard: View {
    let video: VideoSummary
    var context: PlaybackContext = .none
    var showChannel = true
    /// Called with the updated summary once a dismiss/undismiss round trip
    /// succeeds. See ``DismissMenuItem``.
    var onDismissChange: ((VideoSummary) -> Void)?

    @Environment(TVPlayerCoordinator.self) private var player

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button {
                player.play(video, context: context)
            } label: {
                TVVideoThumbnail(video: video)
            }
            .buttonStyle(.card)
            .opacity(video.watched ? 0.65 : 1)
            .accessibilityLabel(video.title)
            // tvOS activates a `.contextMenu` on a focused card with the
            // remote's long-press — the same gesture the phone and iPad use.
            .contextMenu { DismissMenuItem(video: video, onChange: onDismissChange) }

            VStack(alignment: .leading, spacing: 4) {
                Text(video.title)
                    .font(.headline)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                Text(meta)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.bottom, TVMetrics.focusPadding)
    }

    private var meta: String {
        var parts: [String] = []
        if showChannel { parts.append(video.channel.name) }
        parts.append(video.watched ? Fmt.seenLabel(video.lastPlayedAt) : Fmt.relativeDay(video.published))
        return parts.joined(separator: " · ")
    }
}

/// A channel tile: the avatar, the name and how much of it is unseen.
struct TVChannelCard: View {
    let channel: ChannelSummary

    var body: some View {
        NavigationLink(value: TVRoute.channel(channel.id)) {
            VStack(spacing: 14) {
                ChannelAvatar(path: channel.thumbUrl, name: channel.name, size: 120)
                Text(channel.name)
                    .font(.headline)
                    .lineLimit(2)
                    .multilineTextAlignment(.center)
                HStack(spacing: 8) {
                    Text(Fmt.plural(channel.videoCount, "video"))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    TVUnseenBadge(count: channel.unseenCount)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 24)
        }
        .buttonStyle(.card)
        .padding(.bottom, TVMetrics.focusPadding)
    }
}

/// A playlist tile. A music playlist says so, because it plays as audio and
/// records no watch state at all.
struct TVPlaylistCard: View {
    let playlist: PlaylistSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            NavigationLink(value: TVRoute.playlist(playlist.id)) {
                MediaImage(path: playlist.thumbUrl)
                    .aspectRatio(16 / 9, contentMode: .fill)
                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .overlay(alignment: .topTrailing) { badges }
            }
            .buttonStyle(.card)

            VStack(alignment: .leading, spacing: 4) {
                Text(playlist.name)
                    .font(.headline)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                Text(meta)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.bottom, TVMetrics.focusPadding)
    }

    @ViewBuilder
    private var badges: some View {
        HStack(spacing: 6) {
            if playlist.pinned { Image(systemName: "pin.fill") }
            if playlist.music { Image(systemName: "music.note") }
        }
        .font(.caption.weight(.bold))
        .foregroundStyle(.white)
        .padding(8)
    }

    private var meta: String {
        var parts = [Fmt.plural(playlist.videoCount, "video")]
        if playlist.totalDuration > 0 { parts.append(Fmt.durationLong(playlist.totalDuration)) }
        if !playlist.music {
            let remaining = Fmt.remainingUnseen(videoCount: playlist.videoCount, seenCount: playlist.seenCount)
            if remaining > 0 { parts.append("\(Fmt.count(remaining)) unseen") }
        }
        return parts.joined(separator: " · ")
    }
}
