import FlimmKit
import SwiftUI

/// One channel: its banner, what it is in, and its videos.
///
/// The "In feeds:" control from the phone is deliberately absent — feed
/// membership is editing, and editing happens elsewhere — but which feeds the
/// channel is already in is still worth showing, because it explains why its
/// videos turn up where they do.
struct TVChannelDetailView: View {
    let channelId: String

    @Environment(AppModel.self) private var app
    @Environment(TVPlayerCoordinator.self) private var player

    @State private var channel: Channel?
    @State private var pager: Pager<VideoSummary>?
    @State private var view: ChannelView = .all
    @State private var error: String?
    @State private var isMarkingSeen = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                header
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .task { await loadChannel() }
        .task(id: view) { await reload() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 24) {
            if let banner = channel?.summary.bannerUrl, !banner.isEmpty {
                MediaImage(path: banner)
                    .aspectRatio(6 / 1, contentMode: .fill)
                    .frame(maxWidth: .infinity)
                    .frame(height: 200)
                    .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
            HStack(alignment: .center, spacing: 26) {
                ChannelAvatar(path: channel?.summary.thumbUrl, name: channel?.name ?? "", size: 120)
                VStack(alignment: .leading, spacing: 6) {
                    Text(channel?.name ?? "Channel")
                        .font(.system(size: 48, weight: .bold))
                    Text(meta)
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 40)
                controls
            }
        }
        .padding(.top, 20)
    }

    private var meta: String {
        guard let summary = channel?.summary else { return "" }
        var parts = [Fmt.plural(summary.videoCount, "video")]
        if summary.unseenCount > 0 { parts.append("\(Fmt.count(summary.unseenCount)) unseen") }
        if !summary.feeds.isEmpty {
            parts.append("in " + summary.feeds.map(\.name).joined(separator: ", "))
        }
        return parts.joined(separator: " · ")
    }

    private var controls: some View {
        HStack(spacing: 18) {
            Picker("Show", selection: $view) {
                Text("All").tag(ChannelView.all)
                Text("Unseen").tag(ChannelView.unseen)
            }
            .pickerStyle(.segmented)
            .frame(maxWidth: 340)

            Button {
                Task { await shuffle() }
            } label: {
                Label("Shuffle", systemImage: "shuffle")
            }
            .disabled(pager?.items.isEmpty != false)

            Button {
                Task { await markSeen() }
            } label: {
                Label("Mark seen", systemImage: "checkmark.circle")
            }
            .disabled(isMarkingSeen)
        }
    }

    @ViewBuilder
    private var content: some View {
        if let error {
            TVErrorState(message: error) { Task { await loadChannel() } }
        } else if let pager {
            if pager.items.isEmpty && pager.hasLoaded {
                TVEmptyState(
                    icon: view == .unseen ? "checkmark.circle" : "film",
                    title: view == .unseen ? "All caught up" : "No videos"
                )
            } else {
                TVVideoGrid(pager: pager, context: .channel(channelId), showChannel: false)
            }
        } else {
            TVLoadingState()
        }
    }

    // MARK: - Actions

    private func loadChannel() async {
        do {
            channel = try await app.client.channel(channelId)
            error = nil
        } catch {
            self.error = AppModel.message(for: error)
        }
    }

    private func reload() async {
        let client = app.client
        let id = channelId
        let view = view
        let key = "tv-channel:\(id):\(view.rawValue)"
        if let cached: Pager<VideoSummary> = app.pagers.existing(key) {
            pager = cached
            return
        }
        let next = Pager<VideoSummary> { page in
            try await client.channelVideos(id, view: view, page: page)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
    }

    private func markSeen() async {
        isMarkingSeen = true
        defer { isMarkingSeen = false }
        try? await app.client.markChannelSeen(channelId)
        app.pagers.removeAll()
        await app.refreshFeeds()
        await loadChannel()
        await reload()
    }

    private func shuffle() async {
        guard let anchor = pager?.items.first else { return }
        await TVShuffle.start(from: anchor.id, source: .channel(channelId), client: app.client, player: player)
    }
}
