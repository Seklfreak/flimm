import Foundation
import Observation

/// The playback settings that belong to the device rather than the account.
///
/// Everything else a viewer can change — autoplay, speed, subtitles, sponsor
/// skipping — is a server preference and follows the account between clients
/// (`PATCH /me/prefs`). Video quality does not: it is about this screen and
/// this network, so it lives in `UserDefaults` and stays here.
@MainActor
@Observable
public final class PlaybackSettings {
    /// The `UserDefaults` key, stable across releases.
    public static let videoQualityKey = "videoQuality"

    /// Applies to the next thing that starts playing, and to a switch made
    /// mid-video — the player re-resolves it against the video's ladder.
    public var videoQuality: QualityPreference {
        didSet {
            guard videoQuality != oldValue else { return }
            defaults.set(videoQuality.rawValue, forKey: Self.videoQualityKey)
        }
    }

    @ObservationIgnored private let defaults: UserDefaults

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        let stored = defaults.string(forKey: Self.videoQualityKey)
        self.videoQuality = stored.flatMap(QualityPreference.init(rawValue:)) ?? .auto
    }
}
