import AVFoundation
import MediaPlayer
import UIKit

/// What a paused screen and the Home app's remote widget show while a music
/// playlist plays as audio only.
struct TVNowPlayingState {
    let title: String
    let artist: String
    let duration: Double
    let position: Double
    let rate: Double
    let artwork: UIImage?
}

/// `MPNowPlayingInfoCenter` and the audio session.
///
/// `AVPlayerViewController` publishes its own metadata while a video is on
/// screen, so this exists for the audio-only path — where there is no video
/// layer, the screen saver comes up, and this is all that is left to identify
/// what is playing.
@MainActor
enum TVNowPlaying {
    /// The playback position last published, so a tick can be skipped.
    private static var lastPublished: Double = -10

    /// `.playback` is what keeps audio running once the display sleeps.
    static func configureAudioSession() {
        let session = AVAudioSession.sharedInstance()
        try? session.setCategory(.playback, mode: .moviePlayback)
        try? session.setActive(true)
    }

    static func deactivateAudioSession() {
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    /// Publishes what is playing; see the phone's `NowPlayingController` for
    /// why a routine tick is throttled and what `force` is for.
    static func update(_ state: TVNowPlayingState, force: Bool) {
        guard force || abs(state.position - lastPublished) >= 2 else { return }
        lastPublished = state.position
        var info: [String: Any] = [
            MPMediaItemPropertyTitle: state.title,
            MPMediaItemPropertyArtist: state.artist,
            MPMediaItemPropertyPlaybackDuration: state.duration,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: state.position,
            MPNowPlayingInfoPropertyPlaybackRate: state.rate
        ]
        if let artwork = state.artwork {
            info[MPMediaItemPropertyArtwork] = MPMediaItemArtwork(boundsSize: artwork.size) { _ in artwork }
        }
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info
    }

    static func clear() {
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
    }
}
