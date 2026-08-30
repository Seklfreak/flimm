import FlimmKit
import SwiftUI

/// The archived comments for a video, paged.
///
/// One store for both platforms: the phone shows it under the video, the TV in
/// the Info panel, and neither decides anything about a comment beyond how big
/// the text is. Loading starts when something asks — the phone when the
/// section is opened, the TV when the tab is — because comments are the
/// longest thing attached to a video and the least often wanted.
@MainActor
@Observable
final class CommentsStore {
    private(set) var comments: [VideoComment] = []
    private(set) var total = 0
    private(set) var isLoading = false
    private(set) var hasMore = false
    private(set) var failed = false

    @ObservationIgnored private var videoID = ""
    @ObservationIgnored private var nextPage = 0
    @ObservationIgnored private var loaded = false

    /// Loads the first page, once per video. Calling it again for the same
    /// video is free, which is what lets a view ask on every appearance.
    func load(videoID: String, client: APIClient) async {
        if self.videoID != videoID {
            self.videoID = videoID
            comments = []
            total = 0
            nextPage = 0
            hasMore = false
            failed = false
            loaded = false
        }
        guard !loaded, !isLoading else { return }
        await fetch(client: client)
    }

    func loadMore(client: APIClient) async {
        guard hasMore, !isLoading else { return }
        await fetch(client: client)
    }

    private func fetch(client: APIClient) async {
        isLoading = true
        defer { isLoading = false }
        do {
            let page = try await client.comments(videoID, page: nextPage)
            comments += page.items
            total = max(total, Int(page.total))
            hasMore = page.hasMore
            nextPage += 1
            loaded = true
            failed = false
        } catch {
            // Leaving the screen mid-request is not a failure to report;
            // anything else is worth saying so about and worth retrying, but
            // never worth taking the video down with it.
            guard !Task.isCancelled, (error as? APIError) != .cancelled else { return }
            failed = true
        }
    }
}
