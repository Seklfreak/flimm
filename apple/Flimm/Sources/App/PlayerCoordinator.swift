import FlimmKit
import Foundation
import Observation
import SwiftUI

/// What to open the player on.
///
/// The context travels with playback exactly as it does in the web client's
/// URL: it is what makes previous/next, autoplay and a shuffled run agree with
/// each other. `startAt` exists only for a subtitle hit — resume is the default
/// action everywhere else and needs no parameter.
struct PlayRequest: Identifiable, Hashable {
    let id = UUID()
    var videoId: String
    var context: PlaybackContext = .none
    var startAt: Double?
}

/// Owns the watching session, not just the request for one.
///
/// The phone presents the player over the tab bar and the iPad pushes it into
/// the detail column, so the *view* is built twice — and is rebuilt again every
/// time an iPad window resize flips the size class. The ``WatchModel`` (and
/// with it the `AVPlayer`, the audio session and the progress heartbeat) lives
/// here instead, so none of that interrupts playback.
@MainActor
@Observable
final class PlayerCoordinator {
    private(set) var request: PlayRequest?
    private(set) var model: WatchModel?
    /// Set by the full-screen control and the `f` key. On iPad it also collapses
    /// the sidebar, which is what "full screen" means in a split view.
    var isFullScreen = false

    @ObservationIgnored private weak var app: AppModel?
    /// The device's own playback settings — quality. One instance is shared
    /// with the settings screen, so a change made there is the one the next
    /// video reads.
    @ObservationIgnored private var playback = PlaybackSettings()

    /// Called when the signed-in `AppModel` appears or is replaced.
    func configure(app: AppModel?, playback: PlaybackSettings) {
        self.app = app
        self.playback = playback
        if app == nil { dismiss() }
    }

    func play(_ videoId: String, context: PlaybackContext = .none, startAt: Double? = nil) {
        guard let app else { return }
        let next = PlayRequest(videoId: videoId, context: context, startAt: startAt)
        let leaving = model
        Task { await leaving?.tearDown() }
        let created = WatchModel(request: next, app: app, playback: playback)
        model = created
        request = next
        isFullScreen = false
        Task { await created.load() }
    }

    func play(_ video: VideoSummary, context: PlaybackContext = .none) {
        play(video.id, context: context)
    }

    func dismiss() {
        guard request != nil || model != nil else { return }
        let leaving = model
        model = nil
        request = nil
        isFullScreen = false
        Task { await leaving?.tearDown() }
    }

    /// Drives both shells' presentation. Popping the iPad detail push writes
    /// `nil` back, which is the same thing as closing the player.
    var presented: Binding<PlayRequest?> {
        Binding(
            get: { self.request },
            set: { value in if value == nil { self.dismiss() } }
        )
    }
}
