import FlimmKit
import SwiftUI

/// The regular-width shell: a persistent sidebar over the same screens the
/// phone shows in tabs (`docs/design.md`, *Platforms*).
///
/// The sidebar is the web client's, in the same order: the feeds with their
/// unseen counts and the pinned one first, then the library, then pinned
/// playlists, with Settings pinned to the bottom. Selecting a row only writes
/// to ``NavigationModel``; the detail column renders whatever that says, which
/// is why a rotation or a Split View resize keeps the user where they were.
struct RootSplitView: View {
    @Environment(AppModel.self) private var app
    @Environment(NavigationModel.self) private var nav
    @Environment(PlayerCoordinator.self) private var player

    @State private var columnVisibility: NavigationSplitViewVisibility = .automatic

    var body: some View {
        NavigationSplitView(columnVisibility: $columnVisibility) {
            sidebar
        } detail: {
            NavigationStack(path: nav.path(for: nav.tab)) {
                SectionRoot(tab: nav.tab)
                    .flimmDestinations()
                    // A push rather than a cover: the player gets the whole
                    // detail column and the sidebar stays put behind Back.
                    .navigationDestination(item: player.presented) { _ in
                        WatchView()
                    }
            }
            .remoteBar()
        }
        .navigationSplitViewStyle(.balanced)
        .tint(Palette.accent)
        // "Full screen" in a split view means the sidebar has to go too. The
        // `onAppear` covers arriving here from the compact shell mid-playback,
        // which builds this view with the flag already set.
        .onChange(of: player.isFullScreen) { _, full in
            columnVisibility = full ? .detailOnly : .automatic
        }
        .onAppear {
            columnVisibility = player.isFullScreen ? .detailOnly : .automatic
        }
    }

    // MARK: - Sidebar

    private var sidebar: some View {
        List(selection: nav.sidebarSelection(launchFeedId: app.launchFeed?.id)) {
            Section("Feeds") {
                ForEach(sortedFeeds) { feed in
                    FeedSidebarRow(feed: feed)
                        .tag(NavigationModel.SidebarItem.feed(feed.id))
                }
                sidebarButton("Manage feeds", icon: "slider.horizontal.3") { nav.push(.feedManager) }
            }
            Section("Library") {
                ForEach([NavigationModel.Tab.channels, .playlists, .history]) { tab in
                    Label(tab.title, systemImage: tab.icon)
                        .tag(NavigationModel.SidebarItem.tab(tab))
                }
            }
            if !app.pinnedPlaylists.isEmpty {
                Section("Pinned") {
                    ForEach(app.pinnedPlaylists) { playlist in
                        sidebarButton(
                            playlist.name,
                            icon: playlist.music ? "music.note" : "list.and.film"
                        ) {
                            nav.openPlaylist(playlist.id)
                        }
                    }
                }
            }
        }
        .listStyle(.sidebar)
        .refreshable { await app.load() }
        .navigationTitle("Flimm")
        .safeAreaInset(edge: .bottom, spacing: 0) { settingsBar }
    }

    /// Pinned first, otherwise the order the server returns — that order is the
    /// user's own, set in the feed manager.
    private var sortedFeeds: [Feed] {
        app.feeds.enumerated()
            .sorted { lhs, rhs in
                lhs.element.pinned == rhs.element.pinned
                    ? lhs.offset < rhs.offset
                    : lhs.element.pinned
            }
            .map(\.element)
    }

    private func sidebarButton(_ title: String, icon: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Label(title, systemImage: icon)
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private var settingsBar: some View {
        VStack(spacing: 0) {
            Divider()
            Button { nav.openSettings() } label: {
                Label("Settings", systemImage: "gearshape")
                    .font(.subheadline.weight(.semibold))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .padding(.horizontal, 20)
            .padding(.vertical, 14)
        }
        .background(.bar)
    }
}

/// One feed in the sidebar, with the unseen count the web client shows.
struct FeedSidebarRow: View {
    let feed: Feed

    var body: some View {
        HStack(spacing: 8) {
            Label {
                Text(feed.name).lineLimit(1)
            } icon: {
                Image(systemName: icon)
            }
            Spacer(minLength: 0)
            UnseenBadge(count: feed.unseenCount)
        }
    }

    private var icon: String {
        if feed.pinned { return "pin.fill" }
        return feed.isEverything ? "infinity" : "square.stack.3d.up"
    }
}
