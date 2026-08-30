import FlimmKit
import SwiftUI

/// The top tab bar the focus engine drives: Feeds · Channels · Playlists ·
/// History · Search · Settings, as `docs/design.md` describes for Apple TV.
///
/// Each tab owns its own `NavigationStack`. There is no size class to flip
/// here and no multitasking, so unlike the iPad there is nothing to be gained
/// from hoisting the stacks into a shared model.
struct TVShell: View {
    @Environment(AppModel.self) private var app
    @Environment(TVPlayerCoordinator.self) private var player

    /// Debug builds can open straight onto a tab (`FLIMM_OPEN_TAB=settings`),
    /// which is how a screen deep in the shell gets looked at in a simulator
    /// without a remote. A shipped app always opens on Feeds.
    @State private var tab: TVTab = {
        #if DEBUG
        if let raw = ProcessInfo.processInfo.environment["FLIMM_OPEN_TAB"], let tab = TVTab(rawValue: raw) {
            return tab
        }
        #endif
        return .feeds
    }()

    var body: some View {
        TabView(selection: $tab) {
            ForEach(TVTab.allCases) { item in
                NavigationStack {
                    TVSectionRoot(tab: item)
                        .tvDestinations()
                }
                .tabItem { Label(item.title, systemImage: item.icon) }
                .tag(item)
            }
        }
        .task {
            await app.loadIfNeeded()
            // The Home screen's row is written here rather than only from the
            // Feeds screen, so it does not depend on where the viewer goes.
            await TopShelfRefresh.publishLaunchFeed(app: app)
        }
        // Full-bleed: the player covers the tab bar rather than sitting under
        // it, which is what a TV player is expected to do.
        .fullScreenCover(item: player.presented) { _ in
            TVWatchView()
        }
    }
}

enum TVTab: String, Hashable, CaseIterable, Identifiable {
    case feeds, channels, playlists, history, search, settings

    var id: String { rawValue }

    var title: String {
        switch self {
        case .feeds: "Feeds"
        case .channels: "Channels"
        case .playlists: "Playlists"
        case .history: "History"
        case .search: "Search"
        case .settings: "Settings"
        }
    }

    var icon: String {
        switch self {
        case .feeds: "square.stack.3d.up"
        case .channels: "person.2"
        case .playlists: "list.and.film"
        case .history: "clock.arrow.circlepath"
        case .search: "magnifyingglass"
        case .settings: "gearshape"
        }
    }
}

struct TVSectionRoot: View {
    let tab: TVTab

    var body: some View {
        switch tab {
        case .feeds: TVFeedsView()
        case .channels: TVChannelsView()
        case .playlists: TVPlaylistsView()
        case .history: TVHistoryView()
        case .search: TVSearchView()
        case .settings: TVSettingsView()
        }
    }
}
