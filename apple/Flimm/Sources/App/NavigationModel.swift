import FlimmKit
import Observation
import SwiftUI

/// The one place the app keeps "where am I".
///
/// The phone shell (`TabView`) and the iPad shell (`NavigationSplitView`) are
/// two renderings of *this* state, never two states of their own: iPad
/// multitasking flips the horizontal size class mid-flow, which tears one shell
/// down and builds the other, and anything held in a shell's `@State` would go
/// with it. Section, per-section stack and the chosen feed live here so a
/// rotation or a Split View resize lands on the same screen.
@MainActor
@Observable
final class NavigationModel {
    /// A top-level section: a tab on the phone, a sidebar row on iPad.
    enum Tab: String, Hashable, CaseIterable, Identifiable {
        case feeds, channels, playlists, history

        var id: String { rawValue }

        var title: String {
            switch self {
            case .feeds: "Feeds"
            case .channels: "Channels"
            case .playlists: "Playlists"
            case .history: "History"
            }
        }

        var icon: String {
            switch self {
            case .feeds: "square.stack.3d.up"
            case .channels: "person.2"
            case .playlists: "list.and.film"
            case .history: "clock.arrow.circlepath"
            }
        }
    }

    /// What the iPad sidebar highlights. Pinned playlists and Settings are
    /// pushes onto a section's stack rather than sections of their own, so both
    /// shells can express them and a size-class flip loses nothing.
    enum SidebarItem: Hashable {
        case feed(String)
        case tab(Tab)
    }

    /// Debug builds can open straight onto a tab (`FLIMM_OPEN_TAB=settings`),
    /// so a screen deep in the shell can be looked at in a simulator without
    /// tapping through it. A shipped app always opens on Feeds.
    var tab: Tab = {
        #if DEBUG
        if let raw = ProcessInfo.processInfo.environment["FLIMM_OPEN_TAB"], let tab = Tab(rawValue: raw) {
            return tab
        }
        #endif
        return .feeds
    }()
    /// Which feed the Feeds screen shows; `nil` until the launch feed is known.
    var feedId: String?
    /// The Feeds screen's Unseen/All override. `nil` means "whatever
    /// the feed's own `hide_seen` says" — the server's default, not ours.
    var feedView: FeedView?
    /// Driven by ⌘F and `/`; each screen scopes it to itself so only the
    /// visible one opens its search field.
    var isSearchPresented = false

    private var stacks: [Tab: NavigationPath] = [:]

    init() {
        #if DEBUG
        // The sibling of `FLIMM_OPEN_TAB`, for a screen that lives *behind* a
        // tab: `FLIMM_OPEN_ROUTE=stats` opens the tab's stack on that screen.
        // Some screens (Stats, the feed manager) are otherwise unreachable in a
        // simulator, which cannot tap. A shipped app has no such door.
        if let raw = ProcessInfo.processInfo.environment["FLIMM_OPEN_ROUTE"] {
            var path = NavigationPath()
            switch raw {
            case "stats": path.append(Route.stats)
            case "settings": path.append(Route.settings)
            case "feeds": path.append(Route.feedManager)
            // The editor itself, empty: the one screen with the notify
            // switch, which is otherwise two taps behind the manager.
            case "new-feed":
                path.append(Route.feedManager)
                path.append(Route.feedEditor(feedId: nil))
            default: break
            }
            if !path.isEmpty { stacks[tab] = path }
        }
        #endif
    }

    // MARK: - Stacks

    func path(for tab: Tab) -> Binding<NavigationPath> {
        Binding(
            get: { self.stacks[tab] ?? NavigationPath() },
            set: { self.stacks[tab] = $0 }
        )
    }

    /// Pushes onto the section currently on screen.
    func push(_ route: Route) {
        var path = stacks[tab] ?? NavigationPath()
        path.append(route)
        stacks[tab] = path
    }

    func popToRoot(_ tab: Tab) {
        stacks[tab] = NavigationPath()
    }

    // MARK: - Selection

    func select(_ tab: Tab) {
        // Re-selecting the section you are already in pops to its root, which
        // is what both a tab bar and a sidebar are expected to do.
        if self.tab == tab {
            popToRoot(tab)
        } else {
            self.tab = tab
        }
    }

    func select(feed id: String) {
        if feedId != id { feedView = nil }
        feedId = id
        popToRoot(.feeds)
        tab = .feeds
    }

    /// A pinned playlist in the sidebar: the Playlists section with the
    /// playlist pushed, so the phone shows exactly the same thing.
    func openPlaylist(_ id: String) {
        tab = .playlists
        stacks[.playlists] = NavigationPath([Route.playlist(id)])
    }

    func openSettings() {
        push(.settings)
    }

    // MARK: - Sidebar

    func sidebarSelection(launchFeedId: String?) -> Binding<SidebarItem?> {
        Binding(
            get: {
                guard self.tab == .feeds else { return .tab(self.tab) }
                guard let id = self.feedId ?? launchFeedId else { return nil }
                return .feed(id)
            },
            set: { item in
                switch item {
                case .feed(let id): self.select(feed: id)
                case .tab(let tab): self.select(tab)
                case nil: break
                }
            }
        )
    }

    // MARK: - Search

    /// `true` only while `tab` is on screen, so ⌘F opens the search field of
    /// the visible screen and of no other.
    func searchPresented(for tab: Tab) -> Binding<Bool> {
        Binding(
            get: { self.tab == tab && self.isSearchPresented },
            set: { value in
                guard self.tab == tab else { return }
                self.isSearchPresented = value
            }
        )
    }

    func focusSearch() {
        isSearchPresented = true
    }
}
