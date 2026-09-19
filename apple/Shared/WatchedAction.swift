import FlimmKit
import SwiftUI

/// "Mark seen" / "Mark unseen" on a video that is not open in the player:
/// clearing something off an unseen list without playing it, or putting a
/// video back on one. This is the one place the round trip is made, because
/// every video card and row — iPhone, iPad and Apple TV alike — offers the
/// same action the same way.
///
/// Unlike "Not interested" this *is* watch state: it is written back to
/// TubeArchivist and follows the viewer into every other client, which is the
/// difference `docs/design.md` draws between the two.
///
/// `nil` on failure, so a caller can leave the card exactly as it was rather
/// than claiming a change that did not stick.
@MainActor
func toggleWatched(_ video: VideoSummary, client: APIClient) async -> VideoSummary? {
    let watched = !video.watched
    do {
        try await client.setWatched(video.id, watched: watched)
    } catch {
        return nil
    }
    return video.withWatched(watched)
}

/// The context-menu entry every video card and row offers next to
/// ``DismissMenuItem``: "Mark seen" on an unseen video, "Mark unseen" on one
/// already watched. tvOS opens a `.contextMenu` with the remote's long-press
/// and the phone and iPad with a hold, so one view covers all three.
///
/// `onChange` hands the caller the updated summary once the round trip
/// succeeds. Every list keeps the card and patches it in place — a seen video
/// is still a member of the feed, channel, playlist or search result it was
/// found in, and dropping it would take away the way back.
struct WatchedMenuItem: View {
    let video: VideoSummary
    var onChange: ((VideoSummary) -> Void)?

    @Environment(AppModel.self) private var app

    var body: some View {
        Button {
            Task {
                guard let updated = await toggleWatched(video, client: app.client) else { return }
                onChange?(updated)
            }
        } label: {
            if video.watched {
                Label("Mark unseen", systemImage: "circle")
            } else {
                Label("Mark seen", systemImage: "checkmark.circle")
            }
        }
    }
}
