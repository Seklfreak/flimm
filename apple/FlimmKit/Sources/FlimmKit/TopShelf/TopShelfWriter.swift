import Foundation

/// Builds the top shelf's snapshot from videos the app already has.
///
/// It is deliberately given a list rather than asked to fetch one: the feed
/// screen has just loaded exactly what the shelf should show, and a shelf that
/// made its own request would be a second answer to "what is in this feed" —
/// occasionally a different one.
///
/// **The artwork is a URL, not a file.** tvOS draws the shelf in its own
/// process and fetches artwork itself, with none of our headers — and an App
/// Group on tvOS shares preferences rather than a writable directory, so there
/// is nowhere to put a downloaded image where the system could read it. Each
/// entry therefore carries an absolute `/media/thumb/...` URL with a media
/// token in it, which is the one credential a fetcher that can set neither a
/// header nor a cookie can still present.
public enum TopShelfWriter {
    /// Rebuilds the snapshot, or throws saying why it could not.
    @discardableResult
    public static func write(
        feedName: String,
        videos: [VideoSummary],
        client: APIClient,
        now: Date = Date(),
        group: String = TopShelfStore.appGroup
    ) async throws -> TopShelfSnapshot {
        // A token the system can use. Without one the row still shows titles,
        // which beats no row at all.
        let token = try? await client.mediaToken()
        let entries = videos.prefix(TopShelfStore.maxEntries).map { video in
            TopShelfEntry(
                videoID: video.id,
                title: video.title,
                channel: video.channel.name,
                imageURL: thumbURL(for: video, token: token, client: client),
                // The bar under a top-shelf item means the same thing as the
                // one on a card: how far in the viewer got.
                progress: video.watched ? 1 : min(max(video.progress, 0), 1),
                duration: video.duration
            )
        }
        let snapshot = TopShelfSnapshot(feedName: feedName, entries: Array(entries), updatedAt: now)
        try TopShelfStore.write(snapshot, group: group)
        return snapshot
    }

    /// The thumbnail as an absolute URL with the media token attached.
    ///
    /// The token expires in twelve hours and the app republishes on every
    /// launch, so the shelf refreshes long before that. An expired one costs
    /// the pictures, not the row.
    private static func thumbURL(for video: VideoSummary, token: String?, client: APIClient) -> String? {
        guard !video.thumbUrl.isEmpty, let token,
              let url = client.mediaURL(video.thumbUrl),
              var components = URLComponents(url: url, resolvingAgainstBaseURL: false) else {
            return nil
        }
        components.queryItems = (components.queryItems ?? []) + [URLQueryItem(name: "media_token", value: token)]
        return components.url?.absoluteString
    }
}
