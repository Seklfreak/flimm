import FlimmKit
import SwiftUI

/// Every push destination in the app. One enum keeps the four tab stacks
/// interchangeable, which is also what the iPad split view will want later.
enum Route: Hashable {
    case channel(String)
    case playlist(String)
    case feedEditor(feedId: String?)
    case feedManager
    case settings
    case stats
}

extension View {
    /// Registers the shared destinations on a `NavigationStack`.
    func flimmDestinations() -> some View {
        navigationDestination(for: Route.self) { route in
            switch route {
            case .channel(let id):
                ChannelDetailView(channelId: id)
            case .playlist(let id):
                PlaylistDetailView(playlistId: id)
            case .feedEditor(let feedId):
                FeedEditorView(feedId: feedId)
            case .feedManager:
                FeedManagerView()
            case .settings:
                SettingsView()
            case .stats:
                StatsView()
            }
        }
    }
}

/// Starting a shuffled run.
///
/// Shuffle is a seed, not a queue: a new seed reshuffles, and the run starts at
/// `nav.first` so the client never has to derive the shuffled order itself.
enum Shuffle {
    @MainActor
    static func start(
        from anchorVideoId: String,
        source: PlaybackContext.Source,
        client: APIClient,
        player: PlayerCoordinator
    ) async {
        let context = PlaybackContext(source: source, shuffleSeed: PlaybackContext.newShuffleSeed())
        guard let nav = try? await client.nav(anchorVideoId, context: context) else { return }
        let start = nav.first?.id ?? anchorVideoId
        player.play(start, context: context)
    }
}
