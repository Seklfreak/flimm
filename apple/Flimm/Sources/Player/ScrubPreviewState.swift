import FlimmKit
import Observation
import SwiftUI
import UIKit

/// The scrub-preview stills for the video on screen: the sprite sheet and the
/// track that says which part of it belongs to which second.
///
/// This is view state, not playback state, which is why it sits here rather
/// than in ``WatchModel``: nothing about what plays depends on it, and a
/// player whose sheet never arrives is a player that scrubs without pictures.
/// The parsing and the waiting are ``ScrubPreview``'s, shared with every other
/// client.
@MainActor
@Observable
final class ScrubPreviewState {
    private(set) var tiles: [PreviewTile] = []
    private(set) var sheet: UIImage?

    /// What ``tiles`` and ``sheet`` were loaded for, so a rotation or a
    /// window resize — which rebuilds the view and re-runs its task — reuses
    /// them instead of blanking the scrubber and fetching again.
    private var loadedPath: String?

    /// Loads a video's stills, waiting out the derivation.
    ///
    /// The caller decides *when*: the first request is what starts the work on
    /// the server, and it is the most expensive thing there — a full decode of
    /// the file — so it waits until the video is genuinely being watched,
    /// exactly as the web player does.
    func load(path: String, client: APIClient) async {
        guard loadedPath != path else { return }
        tiles = []
        sheet = nil
        let loaded = await ScrubPreview.load(trackPath: path, client: client)
        guard !Task.isCancelled, let first = loaded.first else { return }
        let image = await MediaImageStore.shared.image(at: first.sheetPath, client: client)
        guard !Task.isCancelled, let image else { return }
        loadedPath = path
        tiles = loaded
        sheet = image
    }
}
