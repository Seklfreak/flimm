import FlimmKit
import SwiftUI

/// The launch screen: the pinned feed (else the first), with every feed as a
/// focusable chip along the top and a full-bleed grid underneath.
///
/// **Feeds are read-only here.** Which videos a feed contains, in what order
/// and whether seen ones are hidden is the server's answer either way; what the
/// TV gives up is *editing* them, which is a name, a channel multi-select and
/// four toggles — miserable on a remote and already good on the phone, iPad and
/// web. The screen says so rather than leaving someone hunting for the button.
struct TVFeedsView: View {
    @Environment(AppModel.self) private var app
    @Environment(TVPlayerCoordinator.self) private var player

    @State private var feedId: String?
    @State private var feedView: FeedView?
    @State private var pager: Pager<VideoSummary>?
    @State private var isMarkingSeen = false

    private var feed: Feed? {
        app.feeds.first { $0.id == feedId } ?? app.launchFeed
    }

    private var view: FeedView {
        feedView ?? (feed?.hideSeen == true ? .unseen : .all)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                header
                feedPicker
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .task(id: contextKey) { await rebuildPager() }
        // A video finished or marked seen in the player drops this list from
        // the cache; coming back to a stale "Unseen" grid is the bug.
        .reloadsWhenPlayerCloses(request: player.request, isStale: isPagerStale) {
            await rebuildPager()
        }
    }

    /// Whether this screen is showing a pager the cache has since dropped.
    private func isPagerStale() -> Bool {
        guard let feed, let pager else { return false }
        return !app.pagers.holds(pager, forKey: "feed:\(feed.id):\(view.rawValue)")
    }

    /// Identity of "what this screen is showing" — a change means a new query.
    private var contextKey: String { "\(feed?.id ?? "")|\(view.rawValue)" }

    private var header: some View {
        HStack(alignment: .bottom) {
            TVScreenTitle(title: feed?.name ?? "Feeds", subtitle: summary)
            Spacer(minLength: 40)
            controls
        }
        .padding(.top, 20)
    }

    private var summary: String {
        guard let feed else { return "Edit feeds on your phone, iPad or the web." }
        var parts: [String] = []
        if feed.unseenCount > 0 { parts.append("\(Fmt.count(feed.unseenCount)) unseen") }
        parts.append("Edit feeds on your phone")
        return parts.joined(separator: " · ")
    }

    private var controls: some View {
        // The whole row keeps its natural width. Without this the HStack
        // compresses whichever child yields first — which is how "Shuffle"
        // ended up stacked one letter per line next to a picker that had
        // taken the width it asked for.
        HStack(spacing: 18) {
            Picker("Show", selection: Binding(get: { view }, set: { feedView = $0 })) {
                Text("Unseen").tag(FeedView.unseen)
                Text("Continue").tag(FeedView.continue)
                Text("All").tag(FeedView.all)
            }
            .pickerStyle(.segmented)
            // Sized to its labels, never capped: a fixed width truncates them
            // ("Uns…", "Co…") the moment a label or a locale is longer than
            // the guess. The Spacer above absorbs whatever is left over.
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
                Label("Mark all seen", systemImage: "checkmark.circle")
            }
            .disabled(isMarkingSeen || feed == nil)
        }
        .fixedSize(horizontal: true, vertical: false)
    }

    @ViewBuilder
    private var feedPicker: some View {
        if app.feeds.count > 1 {
            ScrollView(.horizontal) {
                HStack(spacing: 18) {
                    ForEach(app.feeds) { candidate in
                        Button {
                            if feedId != candidate.id { feedView = nil }
                            feedId = candidate.id
                        } label: {
                            HStack(spacing: 10) {
                                if candidate.pinned { Image(systemName: "pin.fill") }
                                Text(candidate.name)
                                TVUnseenBadge(count: candidate.unseenCount)
                            }
                        }
                        .buttonStyle(.borderless)
                        .background(
                            candidate.id == feed?.id ? Palette.accent.opacity(0.25) : Color.clear,
                            in: Capsule()
                        )
                    }
                }
                .padding(.vertical, 10)
            }
            .scrollClipDisabled()
        }
    }

    @ViewBuilder
    private var content: some View {
        if let pager {
            if let error = pager.error, pager.items.isEmpty {
                TVErrorState(message: error) { Task { await pager.reload() } }
            } else if pager.isLoading && !pager.hasLoaded {
                TVLoadingState()
            } else if pager.items.isEmpty {
                emptyState
            } else {
                TVVideoGrid(pager: pager, context: .feed(feed?.id ?? Feed.everythingID))
            }
        } else if let error = app.loadError, app.feeds.isEmpty {
            // A failed load is not "no feeds" — offer a retry rather than
            // implying the feeds are gone.
            TVErrorState(message: error) { Task { await app.load() } }
        } else if app.feeds.isEmpty && !app.isLoading {
            TVEmptyState(
                icon: "tray",
                title: "No feeds yet",
                message: "A feed is a named set of channels. Create one on your phone, iPad or the web."
            )
        } else {
            TVLoadingState()
        }
    }

    private var emptyState: some View {
        TVEmptyState(
            icon: view == .unseen ? "checkmark.circle" : "film",
            title: view == .unseen ? "All caught up" : "Nothing here",
            message: view == .unseen
                ? "Every video in this feed has been seen."
                : "This feed has no videos yet. Add channels to it on your phone.",
            actionTitle: view == .unseen ? "Show all" : nil,
            action: view == .unseen ? { feedView = .all } : nil
        )
    }

    // MARK: - Actions

    private func rebuildPager() async {
        guard let feed else { return }
        if feedId == nil { feedId = feed.id }
        let client = app.client
        let id = feed.id
        let view = view
        let key = "feed:\(id):\(view.rawValue)"
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

    private func markSeen() async {
        guard let feed else { return }
        isMarkingSeen = true
        defer { isMarkingSeen = false }
        try? await app.client.markFeedSeen(feed.id)
        // Every cached list's seen state just changed.
        app.pagers.removeAll()
        await app.refreshFeeds()
        await rebuildPager()
    }

    private func shuffle() async {
        guard let feed, let anchor = pager?.items.first else { return }
        await TVShuffle.start(from: anchor.id, source: .feed(feed.id), client: app.client, player: player)
    }
}
