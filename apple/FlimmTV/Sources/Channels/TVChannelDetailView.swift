import FlimmKit
import SwiftUI

/// One channel: its banner, what it is in, its playlists and its videos.
///
/// The "In feeds:" control from the phone is deliberately absent — feed
/// membership is editing, and editing happens elsewhere — but which feeds the
/// channel is already in is still worth showing, because it explains why its
/// videos turn up where they do. Playlists follow the phone's idea (a strip
/// above the videos, not a section of its own screen) but in the TV's own
/// horizontally-scrolling row of the same card the Playlists tab uses, and
/// most channels have none, so the row simply isn't there rather than
/// showing an empty "Playlists" header.
struct TVChannelDetailView: View {
    let channelId: String

    @Environment(AppModel.self) private var app
    @Environment(TVPlayerCoordinator.self) private var player

    @State private var channel: Channel?
    @State private var playlists: [PlaylistSummary] = []
    @State private var pager: Pager<VideoSummary>?
    @State private var view: ChannelView = .all
    @State private var error: String?
    @State private var isMarkingSeen = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                header
                if !playlists.isEmpty { playlistRow }
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .onAppear { Analytics.screen(.channel) }
        .task { await loadChannel() }
        .task(id: view) { await reload() }
        // Same as the feed screen: the player invalidates these lists, and a
        // stale "Unseen" channel is what a viewer notices.
        .reloadsWhenPlayerCloses(request: player.request, isStale: isPagerStale) {
            await reload()
        }
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
            .fixedSize()

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
        // Same reason as the feed screen: the row keeps its natural width so
        // the picker cannot squeeze the buttons into a column of letters.
        .fixedSize(horizontal: true, vertical: false)
    }

    /// The channel's playlists, as a horizontally-scrolling row of the same
    /// card the dedicated Playlists tab uses — a channel with none shows
    /// nothing here, not an empty section.
    private var playlistRow: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Playlists")
                .font(.title3.weight(.semibold))
                .foregroundStyle(.secondary)
            ScrollView(.horizontal) {
                HStack(alignment: .top, spacing: TVMetrics.gridSpacing) {
                    ForEach(playlists) { playlist in
                        TVPlaylistCard(playlist: playlist)
                            .frame(width: 400)
                    }
                }
            }
            .scrollClipDisabled()
        }
        // Section headers carry bottom padding everywhere on the TV: a
        // focused card grows and would otherwise sit on top of the header.
        .padding(.bottom, 4)
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

    /// Whether this screen is showing a pager the cache has since dropped.
    private func isPagerStale() -> Bool {
        guard let pager else { return false }
        return !app.pagers.holds(pager, forKey: "tv-channel:\(channelId):\(view.rawValue)")
    }

    private func loadChannel() async {
        async let detail = app.client.channel(channelId)
        // The videos are the point of this page; a playlists failure must not
        // stop them from showing, so it is fetched alongside but never thrown.
        async let lists = try? app.client.channelPlaylists(channelId)
        do {
            channel = try await detail
            error = nil
        } catch {
            self.error = AppModel.message(for: error)
        }
        playlists = await lists ?? []
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
        let next = Pager<VideoSummary> { page, cursor in
            try await client.channelVideos(id, view: view, page: page, cursor: cursor)
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
