import FlimmKit
import SwiftUI

/// "Not interested": taking a video out of every feed without watching it.
/// This is the one place the round trip is made, because every video card
/// and row — iPhone, iPad and Apple TV alike — offers exactly the same
/// action the same way (see `docs/api.md`'s `dismissed` section).
///
/// `nil` on failure, so a caller can leave the row exactly as it was rather
/// than guessing at a partial success — the same reason the server makes
/// undismiss idempotent.
@MainActor
func toggleDismissed(_ video: VideoSummary, client: APIClient) async -> VideoSummary? {
    let dismiss = !video.dismissed
    do {
        if dismiss {
            try await client.dismiss(video.id)
        } else {
            try await client.undismiss(video.id)
        }
    } catch {
        return nil
    }
    return video.withDismissed(dismiss)
}

/// The context-menu entry every video card and row offers: "Not interested"
/// on a video still in feeds, "Add back to feeds" once it has been
/// dismissed. tvOS activates a `.contextMenu` with the same long-press the
/// phone and iPad use, so one view covers all three.
///
/// `onChange` hands the caller the updated summary once the round trip
/// succeeds. What happens next is the caller's call: ``VideoList`` and
/// ``TVVideoGrid`` drop the card and offer an undo when the surrounding list
/// is a feed (the only place a dismissed video is never shown at all);
/// everywhere else — channel, playlist, search, history — the card just
/// switches to showing "Not in feeds" in place, because that is where the
/// contract says a viewer finds one again.
struct DismissMenuItem: View {
    let video: VideoSummary
    var onChange: ((VideoSummary) -> Void)?

    @Environment(AppModel.self) private var app

    var body: some View {
        Button {
            Task {
                guard let updated = await toggleDismissed(video, client: app.client) else { return }
                onChange?(updated)
            }
        } label: {
            if video.dismissed {
                Label("Add back to feeds", systemImage: "arrow.uturn.backward")
            } else {
                Label("Not interested", systemImage: "hand.thumbsdown")
            }
        }
    }
}
