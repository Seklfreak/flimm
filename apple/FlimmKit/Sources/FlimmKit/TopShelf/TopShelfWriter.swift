import Foundation

/// Builds the top shelf's snapshot from videos the app already has.
///
/// It is deliberately given a list rather than asked to fetch one: the feed
/// screen has just loaded exactly what the shelf should show, and a shelf that
/// made its own request would be a second answer to "what is in this feed" —
/// occasionally a different one.
///
/// The thumbnails are the reason this is not two lines. tvOS draws the shelf
/// in its own process and fetches artwork itself, with none of our headers, so
/// every image has to be pulled with the bearer token and written into the
/// shared container first. They are cached by file name: a video that was on
/// the shelf yesterday costs nothing today.
public enum TopShelfWriter {
    /// Rebuilds the snapshot, or throws saying why it could not.
    ///
    /// Every failure here used to be a `nil` or a swallowed `try?`, which meant
    /// an empty Home screen row and no way to find out which step gave up —
    /// the entitlement, the container, the encode. They are errors now.
    @discardableResult
    public static func write(
        feedName: String,
        videos: [VideoSummary],
        client: APIClient,
        now: Date = Date(),
        group: String = TopShelfStore.appGroup
    ) async throws -> TopShelfSnapshot {
        guard let dir = TopShelfStore.directory(for: group) else { throw TopShelfError.noContainer }
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let wanted = Array(videos.prefix(TopShelfStore.maxEntries))
        let headers = (try? await client.mediaHeaders()) ?? [:]

        var entries: [TopShelfEntry] = []
        for video in wanted {
            let name = imageName(for: video)
            let saved = await ensureImage(
                named: name, path: video.thumbUrl, headers: headers, client: client, in: dir
            )
            entries.append(TopShelfEntry(
                videoID: video.id,
                title: video.title,
                channel: video.channel.name,
                imageName: saved ? name : nil,
                // The bar under a top-shelf item means the same thing as the
                // one on a card: how far in the viewer got.
                progress: video.watched ? 1 : min(max(video.progress, 0), 1),
                duration: video.duration
            ))
        }
        let snapshot = TopShelfSnapshot(feedName: feedName, entries: entries, updatedAt: now)
        try TopShelfStore.write(snapshot, group: group)
        TopShelfStore.pruneImages(keeping: snapshot, group: group)
        return snapshot
    }

    /// Keyed by the thumbnail's path, not the video id: a DeArrow thumbnail is
    /// a *frame* URL that moves when the crowd votes, and a name that ignored
    /// that would keep showing the old picture forever.
    private static func imageName(for video: VideoSummary) -> String {
        let digest = String(format: "%08x", UInt32(truncatingIfNeeded: stableHash(video.thumbUrl)))
        return "\(video.id)-\(digest).jpg"
    }

    /// A hash that is the same on every launch. `Hashable`'s is seeded per
    /// process, so it would rename — and therefore re-download — every image
    /// each time the app started.
    private static func stableHash(_ text: String) -> UInt64 {
        var hash: UInt64 = 0xcbf2_9ce4_8422_2325
        for byte in text.utf8 {
            hash ^= UInt64(byte)
            hash &*= 0x0000_0100_0000_01b3
        }
        return hash
    }

    private static func ensureImage(
        named name: String,
        path: String,
        headers: [String: String],
        client: APIClient,
        in dir: URL
    ) async -> Bool {
        let file = dir.appendingPathComponent(name)
        if FileManager.default.fileExists(atPath: file.path) { return true }
        guard !path.isEmpty, let url = client.mediaURL(path) else { return false }
        var request = URLRequest(url: url)
        for (field, value) in headers { request.setValue(value, forHTTPHeaderField: field) }
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode),
              !data.isEmpty else {
            return false
        }
        return (try? data.write(to: file, options: .atomic)) != nil
    }
}
