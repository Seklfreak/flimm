import FlimmKit
import SwiftUI

/// Every push destination on Apple TV.
///
/// Shorter than the phone's list on purpose: **feeds are read-only here.**
/// Editing a feed means a name, a channel multi-select and four toggles, which
/// is miserable on a remote and already done well on the phone, iPad and web.
/// The TV says so where a feed would otherwise be editable rather than hiding
/// the fact.
enum TVRoute: Hashable {
    case channel(String)
    case playlist(String)
}

extension View {
    func tvDestinations() -> some View {
        navigationDestination(for: TVRoute.self) { route in
            switch route {
            case .channel(let id): TVChannelDetailView(channelId: id)
            case .playlist(let id): TVPlaylistDetailView(playlistId: id)
            }
        }
    }
}

/// Starting a shuffled run.
///
/// Shuffle is a seed, not a queue: a new seed reshuffles, and the run starts at
/// `nav.first` so the client never has to derive the shuffled order itself.
enum TVShuffle {
    @MainActor
    static func start(
        from anchorVideoId: String,
        source: PlaybackContext.Source,
        audioOnly: Bool = false,
        client: APIClient,
        player: TVPlayerCoordinator
    ) async {
        let context = PlaybackContext(
            source: source,
            shuffleSeed: PlaybackContext.newShuffleSeed(),
            audioOnly: audioOnly
        )
        guard let nav = try? await client.nav(anchorVideoId, context: context) else { return }
        player.play(nav.first?.id ?? anchorVideoId, context: context)
    }
}
