import FlimmKit
import Foundation
import Observation

/// Everything the companion screen shows about the video playing elsewhere.
///
/// None of it comes from the session: the session says what is playing and
/// where it has got to, and the description, the chapters and the comments are
/// read from the same endpoints every other screen reads. That is the whole
/// reason the companion is cheap — there is no second copy of a video's detail
/// to keep in step, and a phone reading a video it is not playing is an
/// ordinary request.
@MainActor
@Observable
final class RemoteDetails {
    private(set) var video: Video?
    private(set) var chapters: [Chapter] = []
    private(set) var isLoading = false
    private(set) var failed = false

    @ObservationIgnored private var loaded = ""

    /// Loads what the given video needs, once. Calling it for the same video
    /// again is free, which is what lets the view ask on every change of the
    /// session — including the ones that are only the clock moving.
    func load(videoID: String, client: APIClient) async {
        guard videoID != loaded, !videoID.isEmpty else { return }
        loaded = videoID
        video = nil
        chapters = []
        failed = false
        isLoading = true
        defer { isLoading = false }
        // Chapters are a separate call and an optional one: a video without
        // them is the common case and must not look like a failure.
        async let detail = client.video(videoID)
        async let chapterList = try? await client.chapters(videoID)
        do {
            let (loadedVideo, loadedChapters) = try await (detail, chapterList)
            // The session moved on while this was in flight — a video that
            // finished, or somebody pressing next twice. Whatever came back is
            // about the wrong video now.
            guard loaded == videoID else { return }
            video = loadedVideo
            chapters = loadedChapters?.chapters ?? []
        } catch {
            guard loaded == videoID else { return }
            // Let the next appearance try again rather than caching a failure.
            loaded = ""
            failed = true
        }
    }

    /// The chapter covering a position, for highlighting the one being played.
    func chapter(at seconds: Double) -> Chapter? {
        chapters.last { $0.start <= seconds }
    }
}
