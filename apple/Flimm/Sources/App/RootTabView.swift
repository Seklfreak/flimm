import FlimmKit
import SwiftUI

/// The compact shell: Feeds · Channels · Playlists · History with search in
/// each tab's header — the iPhone layout from `docs/design.md`, and what an
/// iPad falls back to in Slide Over or a narrow Split View.
///
/// Selection and every stack come from ``NavigationModel``, which the regular
/// shell shares, so a size-class flip is a re-render and not a reset.
struct RootTabView: View {
    @Environment(NavigationModel.self) private var nav
    @Environment(PlayerCoordinator.self) private var player

    var body: some View {
        TabView(selection: tabSelection) {
            ForEach(NavigationModel.Tab.allCases) { tab in
                stack(tab)
                    .tabItem { Label(tab.title, systemImage: tab.icon) }
                    .tag(tab)
            }
        }
        .tint(Palette.accent)
        .fullScreenCover(item: player.presented) { _ in
            // A cover has no navigation bar of its own; the stack gives the
            // watch screen its title and, more importantly, a Close button
            // that is there even when the player overlay is not (codec gate,
            // playback failure, controls hidden).
            NavigationStack {
                WatchView()
            }
        }
    }

    /// Writes through the shared model so the iPad sidebar highlights the same
    /// section, and re-selecting a tab pops it to its root.
    private var tabSelection: Binding<NavigationModel.Tab> {
        Binding(get: { nav.tab }, set: { nav.select($0) })
    }

    private func stack(_ tab: NavigationModel.Tab) -> some View {
        NavigationStack(path: nav.path(for: tab)) {
            SectionRoot(tab: tab)
                .flimmDestinations()
        }
        // Above the tab bar, under every screen in the section — including a
        // pushed one, so walking into a channel does not lose the television.
        .remoteBar()
    }
}

/// The root screen of one section, in whichever shell is showing it.
struct SectionRoot: View {
    let tab: NavigationModel.Tab

    var body: some View {
        switch tab {
        case .feeds: FeedsView()
        case .channels: ChannelsView()
        case .playlists: PlaylistsView()
        case .history: HistoryView()
        }
    }
}
