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
            if model.upNext.isEmpty {
                Text("Nothing more in this context.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.upNext) { video in
                    Button {
                        Task { await model.go(to: video.id) }
                    } label: {
                        HStack(alignment: .top, spacing: 12) {
                            VideoThumbnail(video: video, compact: true)
                                .frame(width: 132)
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
                            Spacer(minLength: 0)
                        }
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    private var autoplayBinding: Binding<Bool> {
        Binding(
            get: { model.prefs.autoplay },
            set: { value in Task { await model.setAutoplay(value) } }
        )
    }
}
