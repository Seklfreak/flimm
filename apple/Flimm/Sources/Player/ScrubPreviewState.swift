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
    /// How many times the track has been asked for, for the playback stats
    /// panel. Asking is what makes the server derive the sheet, and a sheet is
    /// one decode of the whole file, so the count is the difference between a
    /// wait and a queue.
    private(set) var asked = 0

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
        asked = 0
        let loaded = await ScrubPreview.load(trackPath: path, client: client) { [weak self] count in
            Task { @MainActor in self?.asked = count }
        }
        guard !Task.isCancelled, let first = loaded.first else { return }
        let image = await MediaImageStore.shared.image(at: first.sheetPath, client: client)
        guard !Task.isCancelled, let image else { return }
        loadedPath = path
        tiles = loaded
        sheet = image
    }

    /// What the playback stats panel says about the sheet.
    func stats(offered: Bool) -> PlaybackStats.Preview {
        let first = tiles.first
        return PlaybackStats.Preview(
            offered: offered,
            tiles: tiles.count,
            every: first.map { $0.end - $0.start } ?? 0,
            width: Int(first?.rect.width ?? 0),
            height: Int(first?.rect.height ?? 0),
            asked: asked
        )
    }
}
