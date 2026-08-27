import FlimmKit
import SwiftUI

/// The launch screen: the pinned feed (else the first one), with a title menu
/// to switch feeds — the phone equivalent of the web sidebar.
///
/// Which videos a feed contains, in what order and whether seen ones are hidden
/// is entirely the server's answer; this screen only picks `view` and pages.
struct FeedsView: View {
    @Environment(AppModel.self) private var app
    @Environment(NavigationModel.self) private var nav
    @Environment(PlayerCoordinator.self) private var player
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass

    @State private var pager: Pager<VideoSummary>?
    @State private var searchText = ""
    @State private var isMarkingSeen = false

    /// Which feed and which view are ``NavigationModel``'s: the iPad sidebar
    /// sets the first, and both have to outlive a size-class flip.
    private var feed: Feed? {
        app.feeds.first { $0.id == nav.feedId } ?? app.launchFeed
    }

    private var feedView: FeedView {
        nav.feedView ?? (feed?.hideSeen == true ? .unseen : .all)
    }

    var body: some View {
        ScrollView {
            if searchText.isEmpty {
                content
            }
        }
        .refreshable { await refresh() }
        .background(Palette.background)
        .navigationTitle(feed?.name ?? "Feeds")
        .navigationBarTitleDisplayMode(.large)
        .toolbar { toolbar }
        .searchable(
            text: $searchText,
            isPresented: nav.searchPresented(for: .feeds),
            placement: .navigationBarDrawer(displayMode: .automatic),
            prompt: "Search videos, channels, subtitles"
        )
        .overlay {
            if !searchText.isEmpty {
                SearchResultsView(query: searchText, feedId: feed?.id)
                    .background(Palette.background)
            }
        }
        .task(id: contextKey) { await rebuildPager() }
        .reloadsWhenPlayerCloses(request: player.request, isStale: isPagerStale) {
            await rebuildPager()
        }
    }

    /// Identity of "what this screen is showing" — a change means a new query.
    private var contextKey: String {
        "\(feed?.id ?? "")|\(feedView.rawValue)"
    }

    /// The key `PagerStore` files this feed/view combination under — distinct
    /// from ``contextKey``, which only exists to retrigger `.task(id:)`.
    private var pagerKey: String? {
        feed.map { "feed:\($0.id):\(feedView.rawValue)" }
    }

    @ViewBuilder
    private var content: some View {
        if let pager {
            if let error = pager.error, pager.items.isEmpty {
                ErrorState(message: error) { Task { await pager.reload() } }
            } else if pager.isLoading && !pager.hasLoaded {
                LoadingState()
            } else if pager.items.isEmpty {
                emptyState
            } else {
                VideoList(pager: pager, context: .feed(feed?.id ?? Feed.everythingID))
            }
        } else if let error = app.loadError, app.feeds.isEmpty {
            // A failed load is not "no feeds" — offer a retry rather than
            // inviting someone to recreate feeds they already have.
            ErrorState(message: error) { Task { await app.load() } }
        } else if app.feeds.isEmpty && !app.isLoading {
            EmptyState(
                icon: "tray",
                title: "No feeds yet",
                message: "A feed is a named set of channels. Create one to start watching."
            )
        } else {
            LoadingState()
        }
    }

    private var emptyState: some View {
        EmptyState(
            icon: feedView == .unseen ? "checkmark.circle" : "film",
            title: feedView == .unseen ? "All caught up" : "Nothing here",
            message: feedView == .unseen
                ? "Every video in this feed has been seen."
                : "This feed has no videos yet. Add channels to it from the feed editor.",
            actionTitle: feedView == .unseen ? "Show all" : nil,
            action: feedView == .unseen ? { nav.feedView = .all } : nil
        )
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        // The sidebar already lists every feed in the regular layout, so the
        // switcher would only repeat it.
        if horizontalSizeClass != .regular {
            ToolbarItem(placement: .topBarLeading) { feedMenu }
        }
        ToolbarItem(placement: .topBarTrailing) { optionsMenu }
    }

    private var feedMenu: some View {
        Menu {
            Picker("Feed", selection: Binding(get: { feed?.id ?? "" }, set: { nav.select(feed: $0) })) {
                ForEach(app.feeds) { candidate in
                    Text(candidate.unseenCount > 0 ? "\(candidate.name)  (\(Fmt.count(candidate.unseenCount)))" : candidate.name)
                        .tag(candidate.id)
                }
            }
            Divider()
            NavigationLink(value: Route.feedEditor(feedId: nil)) {
                Label("New feed", systemImage: "plus")
            }
        } label: {
            Label("Feeds", systemImage: "line.3.horizontal.decrease.circle")
        }
    }

    private var optionsMenu: some View {
        Menu {
            Picker("Show", selection: Binding(get: { feedView }, set: { nav.feedView = $0 })) {
                Text("Unseen").tag(FeedView.unseen)
                Text("Continue watching").tag(FeedView.continue)
                Text("All").tag(FeedView.all)
            }
            Divider()
            Button {
                Task { await shuffle() }
            } label: {
                Label("Shuffle", systemImage: "shuffle")
            }
            .disabled(pager?.items.isEmpty != false)
            Button {
                Task { await markSeen() }
            } label: {
                Label("Mark all seen", systemImage: "checkmark.circle")
            }
            .disabled(isMarkingSeen || feed == nil)
            Divider()
            if let feed, !feed.isEverything {
                NavigationLink(value: Route.feedEditor(feedId: feed.id)) {
                    Label("Edit feed", systemImage: "slider.horizontal.3")
                }
            }
            NavigationLink(value: Route.settings) {
                Label("Settings", systemImage: "gearshape")
            }
        } label: {
            Label("Options", systemImage: "ellipsis.circle")
        }
    }

    // MARK: - Actions

    private func rebuildPager() async {
        guard let feed, let key = pagerKey else { return }
        if nav.feedId == nil { nav.feedId = feed.id }
        let client = app.client
        let id = feed.id
        let view = feedView
        // Already loaded — a size-class flip rebuilt the screen, not the query.
        if let cached: Pager<VideoSummary> = app.pagers.existing(key) {
            pager = cached
            return
        }
        let next = Pager<VideoSummary> { page in
            try await client.feedVideos(id, view: view, page: page)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
    }

    /// Whether this screen is showing a pager the cache has since dropped.
    private func isPagerStale() -> Bool {
        guard let key = pagerKey, let pager else { return false }
        return !app.pagers.holds(pager, forKey: key)
    }

    private func refresh() async {
        await app.refreshFeeds()
        await pager?.reload()
    }

    private func markSeen() async {
        guard let feed else { return }
        isMarkingSeen = true
        defer { isMarkingSeen = false }
        try? await app.client.markFeedSeen(feed.id)
        // Every cached list's seen state just changed.
        app.pagers.removeAll()
        await refresh()
    }

    private func shuffle() async {
        guard let feed, let anchor = pager?.items.first else { return }
        await Shuffle.start(from: anchor.id, source: .feed(feed.id), client: app.client, player: player)
    }
}
