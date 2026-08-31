import FlimmKit
import SwiftUI

/// A channel: its videos, its archived playlists, and the "In feeds:" control
/// that adds or removes it from feeds without leaving the page.
struct ChannelDetailView: View {
    let channelId: String

    @Environment(AppModel.self) private var app
    @Environment(PlayerCoordinator.self) private var player

    @State private var channel: Channel?
    @State private var playlists: [PlaylistSummary] = []
    @State private var pager: Pager<VideoSummary>?
    @State private var channelView: ChannelView = .all
    @State private var error: String?
    @State private var showFeedPicker = false
    /// Set once the admin asked TA to index this channel's playlists; the
    /// discovery runs archive-side, so there is nothing to await here.
    @State private var seriesIndexRequested = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if let channel {
                    header(channel)
                    if !playlists.isEmpty { playlistStrip }
                    if seriesIndexRequested && playlists.isEmpty {
                        Text("Asked TubeArchivist to index this channel's playlists — the discovery runs there and can take a few minutes.")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 16)
                    }
                    Picker("Show", selection: $channelView) {
                        Text("All").tag(ChannelView.all)
                        Text("Unseen").tag(ChannelView.unseen)
                    }
                    .pickerStyle(.segmented)
                    .padding(.horizontal, 16)
                } else if let error {
                    ErrorState(message: error) { Task { await load() } }
                } else {
                    LoadingState()
                }
                videos
            }
            .padding(.vertical, 8)
        }
        .background(Palette.background)
        .refreshable { await load() }
        .navigationTitle(channel?.name ?? "Channel")
        .onAppear { Analytics.screen(.channel) }
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { toolbar }
        .sheet(isPresented: $showFeedPicker) {
            if let channel {
                ChannelFeedsSheet(channel: channel.summary) { await load() }
            }
        }
        .task { await load() }
        .task(id: channelView) { await reloadVideos(force: false) }
        // Same reason as the feed screen: a video finished or marked seen in
        // the player drops this list from the cache, and an "Unseen" channel
        // that still lists it is the bug.
        .reloadsWhenPlayerCloses(request: player.request, isStale: isPagerStale) {
            await reloadVideos(force: false)
        }
    }

    /// Whether this screen is showing a pager the cache has since dropped.
    private func isPagerStale() -> Bool {
        guard let pager else { return false }
        return !app.pagers.holds(pager, forKey: "channel:\(channelId):\(channelView.rawValue)")
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            Menu {
                Button {
                    showFeedPicker = true
                } label: {
                    Label("In feeds…", systemImage: "tray.full")
                }
                Button {
                    Task { await shuffle() }
                } label: {
                    Label("Shuffle", systemImage: "shuffle")
                }
                .disabled(pager?.items.isEmpty != false)
                if playlists.isEmpty, app.me?.isAdmin == true, !seriesIndexRequested {
                    Button {
                        seriesIndexRequested = true
                        Task { try? await app.client.indexChannelPlaylists(channelId) }
                    } label: {
                        Label("Find series (index playlists)", systemImage: "sparkle.magnifyingglass")
                    }
                }
                Button {
                    Task { await markSeen() }
                } label: {
                    Label("Mark all seen", systemImage: "checkmark.circle")
                }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
        }
    }

    private func header(_ channel: Channel) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            if !channel.summary.bannerUrl.isEmpty {
                MediaImage(path: channel.summary.bannerUrl)
                    .aspectRatio(6 / 1, contentMode: .fill)
                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
            }
            HStack(spacing: 12) {
                ChannelAvatar(path: channel.summary.thumbUrl, name: channel.name, size: 52)
                VStack(alignment: .leading, spacing: 2) {
                    Text(channel.name)
                        .font(.title3.bold())
                        .lineLimit(2)
                    Text(headerMeta(channel.summary))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            if !channel.description.isEmpty {
                Text(channel.description)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(4)
            }
            Button {
                showFeedPicker = true
            } label: {
                Label(feedLabel(channel.summary), systemImage: "tray.full")
                    .font(.footnote.weight(.semibold))
            }
            .buttonStyle(.bordered)
        }
        .padding(.horizontal, 16)
    }

    private func headerMeta(_ summary: ChannelSummary) -> String {
        var parts = [Fmt.plural(summary.videoCount, "video")]
        if summary.unseenCount > 0 { parts.append("\(Fmt.count(summary.unseenCount)) unseen") }
        if let last = summary.lastUpload { parts.append("updated \(Fmt.relativeDay(last))") }
        return parts.joined(separator: " · ")
    }

    private func feedLabel(_ summary: ChannelSummary) -> String {
        let names = summary.feeds.filter { $0.id != Feed.everythingID }.map(\.name)
        return names.isEmpty ? "In feeds: none" : "In feeds: \(names.joined(separator: ", "))"
    }

    private var playlistStrip: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Playlists")
                .font(.headline)
                .padding(.horizontal, 16)
            ScrollView(.horizontal) {
                HStack(spacing: 12) {
                    ForEach(playlists) { playlist in
                        NavigationLink(value: Route.playlist(playlist.id)) {
                            PlaylistTile(playlist: playlist)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.horizontal, 16)
            }
            .scrollIndicators(.hidden)
        }
    }

    @ViewBuilder
    private var videos: some View {
        if let pager {
            if pager.items.isEmpty && pager.hasLoaded {
                EmptyState(icon: "film", title: channelView == .unseen ? "Nothing unseen" : "No videos")
            } else {
                VideoList(pager: pager, context: .channel(channelId), showChannel: false)
            }
        }
    }

    // MARK: - Actions

    private func load() async {
        do {
            async let detail = app.client.channel(channelId)
            async let lists = app.client.channelPlaylists(channelId)
            let (loaded, loadedLists) = try await (detail, lists)
            channel = loaded
            playlists = loadedLists
            error = nil
        } catch {
            self.error = AppModel.message(for: error)
        }
        await reloadVideos(force: true)
    }

    /// `force` is pull-to-refresh and mark-seen; everything else is happy with
    /// the list it already has (see ``PagerStore``).
    private func reloadVideos(force: Bool) async {
        let client = app.client
        let id = channelId
        let view = channelView
        let key = "channel:\(id):\(view.rawValue)"
        if let cached: Pager<VideoSummary> = app.pagers.existing(key) {
            pager = cached
            if force { await cached.reload() }
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
        try? await app.client.markChannelSeen(channelId)
        app.pagers.removeAll()
        await load()
        await app.refreshFeeds()
    }

    private func shuffle() async {
        guard let anchor = pager?.items.first else { return }
        await Shuffle.start(from: anchor.id, source: .channel(channelId), client: app.client, player: player)
    }
}

/// The "In feeds:" control as a sheet — `PUT /channels/{id}/feeds` replaces the
/// whole membership set, so the sheet edits a local copy and saves once.
struct ChannelFeedsSheet: View {
    let channel: ChannelSummary
    let onSaved: () async -> Void

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var selection: Set<String> = []
    @State private var isSaving = false

    private var editableFeeds: [Feed] { app.feeds.filter { !$0.isEverything } }

    var body: some View {
        NavigationStack {
            List {
                if editableFeeds.isEmpty {
                    Text("No feeds yet — create one first.")
                        .foregroundStyle(.secondary)
                }
                ForEach(editableFeeds) { feed in
                    Button {
                        if selection.contains(feed.id) { selection.remove(feed.id) } else { selection.insert(feed.id) }
                    } label: {
                        HStack {
                            Text(feed.name)
                            Spacer()
                            if selection.contains(feed.id) {
                                Image(systemName: "checkmark").foregroundStyle(Palette.accent)
                            }
                        }
                    }
                    .buttonStyle(.plain)
                }
            }
            .navigationTitle(channel.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { Task { await save() } }
                        .disabled(isSaving)
                }
            }
        }
        .presentationDetents([.medium, .large])
        .task { selection = Set(channel.feeds.map(\.id).filter { $0 != Feed.everythingID }) }
    }

    private func save() async {
        isSaving = true
        defer { isSaving = false }
        try? await app.client.setChannelFeeds(channel.id, feedIds: Array(selection))
        // Feed membership changes what every feed list contains.
        app.pagers.removeAll()
        await app.refreshFeeds()
        await onSaved()
        dismiss()
    }
}
