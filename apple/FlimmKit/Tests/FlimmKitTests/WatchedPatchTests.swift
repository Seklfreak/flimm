import Foundation
import Testing

@testable import FlimmKit

/// "Mark seen" / "Mark unseen" from a card patches the summary locally rather
/// than refetching the list, so the patch has to land where the server would:
/// `POST /videos/{id}/watched` completes the watch event keeping its position,
/// or clears completion *and* position, and never touches `last_played_at`.
@Suite struct WatchedPatchTests {
    private func summary(watched: Bool, position: Double, progress: Double) -> VideoSummary {
        VideoSummary(
            id: "v1",
            title: "A video",
            channel: VideoChannelRef(id: "UC1", name: "A channel", thumbUrl: ""),
            thumbUrl: "",
            duration: 600,
            watched: watched,
            position: position,
            progress: progress,
            lastPlayedAt: Date(timeIntervalSince1970: 1_000)
        )
    }

    @Test func markingSeenKeepsThePositionAndFillsTheBar() {
        let patched = summary(watched: false, position: 240, progress: 0.4).withWatched(true)
        #expect(patched.watched)
        // A seen video is started over, not resumed — but what it recorded is
        // still what it recorded.
        #expect(patched.position == 240)
        #expect(patched.progress == 1)
        #expect(!patched.isInProgress)
    }

    @Test func markingUnseenClearsPositionAndProgress() {
        let patched = summary(watched: true, position: 240, progress: 1).withWatched(false)
        #expect(!patched.watched)
        #expect(patched.position == 0)
        #expect(patched.progress == 0)
        // Nothing to resume: an unseen video with no position is not "in progress".
        #expect(!patched.isInProgress)
    }

    /// The server deliberately does not bump `last_played_at` on this call, so
    /// toggling from a list must not reorder history under the viewer.
    @Test func neverMovesTheVideoInHistory() {
        let original = summary(watched: false, position: 240, progress: 0.4)
        #expect(original.withWatched(true).lastPlayedAt == original.lastPlayedAt)
        #expect(original.withWatched(false).lastPlayedAt == original.lastPlayedAt)
    }

    @Test func leavesDismissalAlone() {
        let dismissed = summary(watched: false, position: 0, progress: 0).withDismissed(true)
        #expect(dismissed.withWatched(true).dismissed)
    }
}
