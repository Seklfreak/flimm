import FlimmKit
import Foundation
import Observation
import SwiftUI

/// What to open the player on. The context travels with playback exactly as it
/// does in the web client's URL — it is what makes previous/next, autoplay and
/// a shuffled run agree with each other.
struct TVPlayRequest: Identifiable, Hashable {
    let id = UUID()
    var videoId: String
    var context: PlaybackContext = .none
    var startAt: Double?
}

/// Owns the watching session rather than the request for one.
///
/// The player is presented as a full-screen cover, and SwiftUI is free to
/// rebuild that cover's content; the `AVPlayer`, the audio session and the
/// progress heartbeat must not go with it.
@MainActor
@Observable
final class TVPlayerCoordinator {
    private(set) var request: TVPlayRequest?
    private(set) var model: TVWatchModel?

    @ObservationIgnored private weak var app: AppModel?
    /// This Apple TV's own playback settings — quality. One instance, shared
    /// with the settings screen.
    @ObservationIgnored private var playback = PlaybackSettings()

    func configure(app: AppModel?, playback: PlaybackSettings) {
        self.app = app
        self.playback = playback
        if app == nil { dismiss() }
    }

    func play(_ videoId: String, context: PlaybackContext = .none, startAt: Double? = nil) {
        guard let app else { return }
        let next = TVPlayRequest(videoId: videoId, context: context, startAt: startAt)
        let leaving = model
        Task { await leaving?.tearDown() }
        let created = TVWatchModel(request: next, app: app, playback: playback)
        model = created
        request = next
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
        closing = Task { [weak self] in
            await leaving?.tearDown()
            self?.closing = nil
        }
    }

    /// The teardown of the session just closed, still reporting its last
    /// position while `request` is already nil; a list that reloads on the
    /// way back waits for it (see `reloadsWhenPlayerCloses`).
    private(set) var closing: Task<Void, Never>?

    func settle() async {
        await closing?.value
    }

    /// Drives the full-screen cover. Dismissing it is the same thing as
    /// closing the player.
    var presented: Binding<TVPlayRequest?> {
        Binding(
            get: { self.request },
            set: { value in if value == nil { self.dismiss() } }
        )
    }
}
