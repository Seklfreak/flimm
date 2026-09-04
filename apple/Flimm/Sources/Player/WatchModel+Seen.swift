import FlimmKit
import Foundation

/// The two per-user marks a viewer sets on the video being watched. Both
/// flip locally first and let the round trip catch up: they are Flimm's own
/// state, and a button that waits for the network reads as broken.
extension WatchModel {
    func setWatched(_ watched: Bool) async {
        isWatched = watched
        try? await client.setWatched(videoId, watched: watched)
        await app.videoListStateChanged()
    }

    /// Dismiss or restore the video being watched. Says nothing about
    /// playback: it carries on either way (docs/design.md).
    func setDismissed(_ dismissed: Bool) async {
        isDismissed = dismissed
        do {
            if dismissed {
                _ = try await client.dismiss(videoId)
            } else {
                _ = try await client.undismiss(videoId)
            }
        } catch {
            isDismissed = !dismissed
            return
        }
        // Every cached list contains this video too.
        await app.videoListStateChanged()
    }
}
