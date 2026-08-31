import FlimmKit
import SwiftUI

/// Title, metadata, channel and the actions under the video.
struct VideoHeader: View {
    let model: WatchModel
    let video: Video

    @State private var descriptionExpanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(video.title)
                .font(.title3.bold())
                .fixedSize(horizontal: false, vertical: true)
            Text(meta)
                .font(.caption)
                .foregroundStyle(.secondary)
            votes
            HStack(spacing: 10) {
                ChannelAvatar(path: video.channel.thumbUrl, name: video.channel.name, size: 40)
                VStack(alignment: .leading, spacing: 1) {
                    Text(video.channel.name)
                        .font(.subheadline.weight(.bold))
                        .lineLimit(1)
                    Text(channelMeta)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
            }
            actions
            if !video.description.isEmpty {
                description
            }
        }
    }

    /// Counts, not controls: nothing here votes on YouTube's behalf. The
    /// dislike half exists only when the deployment enabled Return YouTube
    /// Dislike and that service knows the video, so it appears and disappears
    /// per video rather than per install (docs/api.md, "Views and votes").
    @ViewBuilder
    private var votes: some View {
        if video.stats.views > 0 || video.stats.likes > 0 || video.stats.dislikes != nil {
            HStack(spacing: 14) {
                if video.stats.views > 0 {
                    Text("\(Fmt.compact(video.stats.views)) views")
                }
                // An archive that recorded no likes shows none, rather than a
                // thumb reading zero. A real zero arrives with a dislike count.
                if video.stats.likes > 0 || video.stats.dislikes != nil {
                    Label(Fmt.compact(video.stats.likes), systemImage: "hand.thumbsup")
                }
                if let dislikes = video.stats.dislikes {
                    Label(Fmt.compact(dislikes), systemImage: "hand.thumbsdown")
                }
            }
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
        }
    }

    private var meta: String {
        var parts = [Fmt.duration(video.duration)]
        if video.height > 0 { parts.append("\(video.height)p") }
        parts.append("added \(Fmt.relativeDay(video.downloaded))")
        return parts.joined(separator: " · ")
    }

    private var channelMeta: String {
        let feeds = video.channel.feeds.filter { $0.id != Feed.everythingID }.map(\.name)
        let count = Fmt.plural(video.channel.videoCount, "video")
        return feeds.isEmpty ? "\(count) · not in a feed" : "\(count) · in \(feeds.joined(separator: ", "))"
    }

    @ViewBuilder
    private var actions: some View {
        HStack(spacing: 10) {
            // Marking a song seen is meaningless: a music playlist records no
            // watch state at all (docs/api.md, "Music playlists").
            if !model.audioOnly {
                let seenLabel = Label(model.isWatched ? "Seen" : "Mark seen", systemImage: "checkmark")
                    .font(.footnote.weight(.semibold))
                if model.isWatched {
                    Button { Task { await model.setWatched(false) } } label: { seenLabel }
                        .buttonStyle(.bordered)
                } else {
                    Button { Task { await model.setWatched(true) } } label: { seenLabel }
                        .buttonStyle(.borderedProminent)
                }
            }
            if let url = URL(string: video.youtubeUrl), !video.youtubeUrl.isEmpty {
                Link(destination: url) {
                    Label("YouTube", systemImage: "arrow.up.right.square")
                        .font(.footnote.weight(.semibold))
                }
                .buttonStyle(.bordered)
            }
            Spacer(minLength: 0)
        }
    }

    private var description: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(video.description)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .lineLimit(descriptionExpanded ? nil : 4)
                .fixedSize(horizontal: false, vertical: true)
            Button(descriptionExpanded ? "Show less" : "Show more") {
                descriptionExpanded.toggle()
            }
            .font(.caption.weight(.semibold))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(Palette.raised, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
    }
}

/// What follows in the current context, with the autoplay preference beside it.
struct UpNextList: View {
    let model: WatchModel
    /// The row's available width in the iPad wide layout's side column;
    /// `nil` under the video at full width (`narrow(_:)`), which always gets
    /// the roomy thumbnail-leading row. In portrait with the sidebar showing
    /// that column can be under 200pt — too narrow for a fixed 132pt
    /// thumbnail to leave the title and channel name a readable width, which
    /// is what broke titles mid-word and truncated every channel name to a
    /// couple of letters. Below the threshold the row stacks instead of
    /// squeezing the text sideways.
    var columnWidth: CGFloat?

    @Environment(AppModel.self) private var app

    /// Set right after "Not interested" drops a row — what the undo banner
    /// acts on. Same shape as ``VideoList``'s, for the same reason.
    @State private var pendingUndo: PendingDismiss?
    /// How many of the previous videos are shown; starts at two, "Show
    /// earlier" walks further back.
    @State private var visiblePrevious = 2

    private struct PendingDismiss: Identifiable {
        let video: VideoSummary
        let index: Int
        var id: String { video.id }
    }

    private var stacked: Bool {
        guard let columnWidth else { return false }
        return columnWidth < 260
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(model.hasContext ? "Up next" : "Similar videos")
                    .font(.headline)
                Spacer()
                Toggle("Autoplay", isOn: autoplayBinding)
                    .labelsHidden()
                Text("Autoplay")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            // No heading, no empty state: the dimmed rows above the raised
            // anchor *are* the previous videos, one continuous list. "Show
            // earlier" sits on top — earlier is upward.
            if model.hasContext, !model.previous.isEmpty {
                if model.previous.count > visiblePrevious || model.hasMorePrevious {
                    Button("Show earlier") {
                        visiblePrevious += 10
                        if model.previous.count < visiblePrevious {
                            Task { await model.loadMorePrevious() }
                        }
                    }
                    .font(.footnote.weight(.semibold))
                    .buttonStyle(.plain)
                    .foregroundStyle(Palette.accent)
                }
                ForEach(model.previous.prefix(visiblePrevious).reversed()) { video in
                    Button {
                        Task { await model.go(to: video.id) }
                    } label: {
                        row(video)
                    }
                    .buttonStyle(.plain)
                    // What was already watched recedes; the row a viewer
                    // would go back for keeps its full weight.
                    .opacity(video.watched ? 0.45 : 1)
                }
            }
            // The anchor: where the viewer is in the context, so the
            // history above and the queue below read as one list.
            if model.hasContext, let current = model.video?.summary {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Now playing")
                        .font(.caption2.weight(.bold))
                        .foregroundStyle(Palette.accent)
                    row(current)
                }
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Palette.raised, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            }
            if model.upNext.isEmpty {
                Text("Nothing more in this context.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.upNext) { video in
                    Button {
                        Task { await model.go(to: video.id) }
                    } label: {
                        row(video)
                    }
                    .buttonStyle(.plain)
                    .contextMenu {
                        DismissMenuItem(video: video, onChange: handleDismiss)
                    }
                }
                if let pendingUndo {
                    DismissUndoBanner(title: pendingUndo.video.title) {
                        Task { await undoDismiss(pendingUndo) }
                    }
                }
            }
        }
        // A step to another video starts a fresh history: show two again.
        .onChange(of: model.video?.id) { _, _ in visiblePrevious = 2 }
    }

    // MARK: - Dismiss / undo

    private func handleDismiss(_ updated: VideoSummary) {
        guard updated.dismissed,
              let index = model.upNext.firstIndex(where: { $0.id == updated.id }) else { return }
        withAnimation {
            pendingUndo = PendingDismiss(video: updated, index: index)
            model.setUpNext(model.upNext.filter { $0.id != updated.id })
        }
        // Every other cached list contains this video too.
        Task { await app.videoListStateChanged() }
    }

    /// Back where it was, so the list a viewer was reading does not reshuffle.
    private func undoDismiss(_ pending: PendingDismiss) async {
        if pendingUndo?.id == pending.id { pendingUndo = nil }
        guard let restored = await toggleDismissed(pending.video, client: app.client) else { return }
        var items = model.upNext
        guard !items.contains(where: { $0.id == restored.id }) else { return }
        items.insert(restored, at: min(pending.index, items.count))
        withAnimation { model.setUpNext(items) }
        await app.videoListStateChanged()
    }

    @ViewBuilder
    private func row(_ video: VideoSummary) -> some View {
        if stacked {
            VStack(alignment: .leading, spacing: 8) {
                VideoThumbnail(video: video, compact: true)
                info(video)
            }
        } else {
            HStack(alignment: .top, spacing: 12) {
                VideoThumbnail(video: video, compact: true)
                    .frame(width: 132)
                info(video)
                Spacer(minLength: 0)
            }
        }
    }

    private func info(_ video: VideoSummary) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(video.title)
                .font(.subheadline.weight(.bold))
                .lineLimit(2)
                .multilineTextAlignment(.leading)
            Text("\(video.channel.name) · \(Fmt.duration(video.duration))")
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
    }

    private var autoplayBinding: Binding<Bool> {
        Binding(
            get: { model.prefs.autoplay },
            set: { value in Task { await model.setAutoplay(value) } }
        )
    }
}
