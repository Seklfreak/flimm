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
        .onAppear { Analytics.screen(.feed) }
        .task(id: contextKey) { await rebuildPager() }
        // A video finished or marked seen in the player drops this list from
        // the cache; coming back to a stale "Unseen" grid is the bug.
        .reloadsWhenPlayerCloses(request: player.request, isStale: isPagerStale) {
            await rebuildPager()
            // Watching something changes the shelf too: that video is either
            // gone from an unseen feed or carrying a resume bar now.
            await publishTopShelf()
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
                        TVFeedChip(feed: candidate, isCurrent: candidate.id == feed?.id) {
                            if feedId != candidate.id { feedView = nil }
                            feedId = candidate.id
                        }
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
        selectDebugFeed()
        guard let feed else { return }
        if feedId == nil { feedId = feed.id }
        let client = app.client
        let id = feed.id
        let view = view
        let key = "feed:\(id):\(view.rawValue)"
        if let cached: Pager<VideoSummary> = app.pagers.existing(key) {
            pager = cached
            await publishTopShelf()
            return
        }
        let next = Pager<VideoSummary> { page, cursor in
            try await client.feedVideos(id, view: view, page: page, cursor: cursor)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
        await publishTopShelf()
    }

    /// Hands the Home screen what this screen is showing — but only for the
    /// feed the app opens on, and only its first page. Browsing another feed
    /// does not change what the shelf should offer.
    private func publishTopShelf() async {
        guard let feed, feed.id == app.launchFeed?.id, let items = pager?.items else { return }
        await TopShelfRefresh.publish(feed: feed, videos: items, client: app.client)
    }

    /// Debug builds can open on a feed *other* than the launch one, so the
    /// picker's selected state can be looked at in a simulator without a
    /// remote to press:
    ///
    ///     SIMCTL_CHILD_FLIMM_OPEN_FEED=Making xcrun simctl launch <device> …
    ///
    /// By name, because a feed's id is a UUID nobody has to hand. A shipped
    /// app has no such door.
    private func selectDebugFeed() {
        #if DEBUG
        guard feedId == nil,
              let name = ProcessInfo.processInfo.environment["FLIMM_OPEN_FEED"],
              let match = app.feeds.first(where: { $0.name == name }) else {
            return
        }
        feedId = match.id
        #endif
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

/// One feed in the picker.
///
/// **Focus and selection are different things, and both have to be visible.**
/// Which feed you are *looking at* is the accent capsule; which chip the remote
/// is *on* is the white one. tvOS normally supplies the second for free, but
/// only for a button whose background it draws — this chip paints its own
/// inside the label (so the fill wraps the words rather than the button's
/// frame), which leaves `.borderless` with nothing to repaint and the row with
/// no focus indication at all. Moving along it then tells you nothing, which is
/// worse than not marking the selection: you cannot press the right thing if
/// you cannot see where you are.
///
/// A focused chip that is *also* the current feed keeps an accent ring, so
/// arriving on it still says "this is the one you are already on".
private struct TVFeedChip: View {
    let feed: Feed
    let isCurrent: Bool
    let select: () -> Void

    @FocusState private var isFocused: Bool

    var body: some View {
        Button(action: select) {
            HStack(spacing: 10) {
                if feed.pinned { Image(systemName: "pin.fill") }
                Text(feed.name)
                    .fontWeight(isCurrent ? .bold : .regular)
                TVUnseenBadge(count: feed.unseenCount)
            }
            .padding(.horizontal, 24)
            .padding(.vertical, 12)
            .background(background, in: Capsule())
            .overlay {
                // The one case a fill cannot carry on its own: focused *and*
                // current, where the white says "you are here" and the ring
                // says "this is what you are watching".
                if isFocused && isCurrent {
                    Capsule().strokeBorder(Palette.accent, lineWidth: 4)
                }
            }
            .foregroundStyle(foreground)
            // The lift tvOS gives a focused control, which reads from a sofa
            // in a way a colour change alone does not.
            .scaleEffect(isFocused ? 1.08 : 1)
            .shadow(radius: isFocused ? 12 : 0)
            .animation(.easeOut(duration: 0.12), value: isFocused)
        }
        .buttonStyle(.borderless)
        .focused($isFocused)
        // Debug builds can start with the remote already on a named chip:
        //
        //     SIMCTL_CHILD_FLIMM_FOCUS_FEED=Everything xcrun simctl launch …
        //
        // Focus is the half of this row that cannot be checked from a
        // screenshot otherwise — nothing in a simulator moves it without a
        // remote, and it is the half that was missing. A shipped app has no
        // such door.
        .task {
            #if DEBUG
            if ProcessInfo.processInfo.environment["FLIMM_FOCUS_FEED"] == feed.name {
                isFocused = true
            }
            #endif
        }
    }

    private var background: Color {
        if isFocused { return .white }
        return isCurrent ? Palette.accent : Palette.raised
    }

    private var foreground: Color {
        if isFocused { return .black }
        return isCurrent ? .white : .primary
    }
}
