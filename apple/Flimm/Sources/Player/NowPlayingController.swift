import AVFoundation
import FlimmKit
import MediaPlayer
import UIKit

/// What the lock screen shows.
struct NowPlayingState {
    let title: String
    let artist: String
    let duration: Double
    let position: Double
    let rate: Double
    let artwork: UIImage?
}

/// Lock screen / Control Centre metadata and the remote transport commands.
///
/// This is what makes audio-only mode worth having on a phone: a music playlist
/// keeps playing with the screen off, with working play/pause, skip and scrub.
@MainActor
final class NowPlayingController {
    var onPlay: (() -> Void)?
    var onPause: (() -> Void)?
    var onNext: (() -> Void)?
    var onPrevious: (() -> Void)?
    var onSeek: ((Double) -> Void)?
    /// Lets the toggle command ask what state playback is actually in.
    var isPlaying: (() -> Bool)?

    private var isRegistered = false

    /// `.playback` also overrides the ringer switch, which a video player wants
    /// regardless of whether it will run in the background.
    static func configureAudioSession() {
        let session = AVAudioSession.sharedInstance()
        try? session.setCategory(.playback, mode: .moviePlayback)
        try? session.setActive(true)
    }

    static func deactivateAudioSession() {
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    func register(hasNext: Bool, hasPrevious: Bool) {
        let center = MPRemoteCommandCenter.shared()
        center.playCommand.isEnabled = true
        center.pauseCommand.isEnabled = true
        center.togglePlayPauseCommand.isEnabled = true
        center.nextTrackCommand.isEnabled = hasNext
        center.previousTrackCommand.isEnabled = hasPrevious
        center.changePlaybackPositionCommand.isEnabled = true

        guard !isRegistered else { return }
        isRegistered = true
        center.playCommand.addTarget { [weak self] _ in
            self?.onPlay?()
            return .success
        }
        center.pauseCommand.addTarget { [weak self] _ in
            self?.onPause?()
            return .success
        }
        center.togglePlayPauseCommand.addTarget { [weak self] _ in
            guard let self else { return .commandFailed }
            if self.isPlaying?() == true { self.onPause?() } else { self.onPlay?() }
            return .success
        }
        center.nextTrackCommand.addTarget { [weak self] _ in
            self?.onNext?()
            return .success
        }
        center.previousTrackCommand.addTarget { [weak self] _ in
            self?.onPrevious?()
            return .success
        }
        center.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let event = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            self?.onSeek?(event.positionTime)
            return .success
        }
    }

    func unregister() {
        let center = MPRemoteCommandCenter.shared()
        for command in [
            center.playCommand, center.pauseCommand, center.togglePlayPauseCommand,
            center.nextTrackCommand, center.previousTrackCommand, center.changePlaybackPositionCommand
        ] {
            command.removeTarget(nil)
        }
        isRegistered = false
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
    }

    func update(_ state: NowPlayingState) {
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
}
