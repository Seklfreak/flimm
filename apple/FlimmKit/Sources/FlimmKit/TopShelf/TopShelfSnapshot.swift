import Foundation

// The Apple TV Home screen's top shelf: the row that appears above the icons
// when Flimm is focused in the top row.
//
// The row is drawn by tvOS, in another process, from what a small extension
// hands it — and that process cannot call the API, has no token, and fetches
// artwork itself with no headers of ours. So the *app* prepares everything:
// it writes this snapshot and the thumbnails into a shared App Group
// container, and the extension is a reader with no network of its own.
//
// The alternative — sharing the keychain so the extension could fetch — would
// have two processes refreshing one OIDC session, and a rotated refresh token
// consumed by the extension is a signed-out app. The cost of this shape is
// that the shelf is only as fresh as the last time someone opened Flimm,
// which is also when it matters.

/// One video as the top shelf shows it.
public struct TopShelfEntry: Codable, Sendable, Hashable, Identifiable {
    public let videoID: String
    public let title: String
    /// The channel name — the top shelf draws it under the title.
    public let channel: String
    /// The thumbnail's file name inside the shared container, or nil when it
    /// could not be fetched. An entry without one still shows, with tvOS's own
    /// placeholder, rather than being dropped.
    public let imageName: String?
    /// 0…1, drawn as the resume bar on the item. 0 for an unwatched video.
    public let progress: Double
    public let duration: Double

    public var id: String { videoID }

    /// The same acronym trap the API models carry: `.convertFromSnakeCase`
    /// turns `video_id` into `videoId`, with a lowercase `d`, which does not
    /// match a property spelled `videoID`. Without this the snapshot encodes
    /// fine and decodes as "key not found" — an empty shelf with no error
    /// anywhere.
    private enum CodingKeys: String, CodingKey {
        case videoID = "videoId"
        case title, channel, imageName, progress, duration
    }

    public init(
        videoID: String,
        title: String,
        channel: String,
        imageName: String?,
        progress: Double,
        duration: Double
    ) {
        self.videoID = videoID
        self.title = title
        self.channel = channel
        self.imageName = imageName
        self.progress = progress
        self.duration = duration
    }
}

/// What the extension reads: one titled row of videos, and when it was made.
public struct TopShelfSnapshot: Codable, Sendable, Hashable {
    /// The feed's name — the row's heading, so the shelf says *which* feed it
    /// is showing rather than presenting an anonymous strip of videos.
    public let feedName: String
    public let entries: [TopShelfEntry]
    public let updatedAt: Date

    public init(feedName: String, entries: [TopShelfEntry], updatedAt: Date) {
        self.feedName = feedName
        self.entries = entries
        self.updatedAt = updatedAt
    }
}

/// Where the snapshot and its images live: the App Group container both the
/// app and the extension can reach.
public enum TopShelfStore {
    /// The App Group. Both the app and the extension carry it as an
    /// entitlement; without it `containerURL` is nil and the shelf simply
    /// stays empty.
    public static let appGroup = "group.dev.winktech.flimm"

    /// The URL scheme the top shelf's actions use to open a video. tvOS has no
    /// browser and no OIDC redirect, so this is the app's only URL type.
    public static let urlScheme = "dev.winktech.flimm.tv"

    /// How many videos the row holds. The top shelf scrolls, but nobody
    /// scrolls a Home screen shelf far, and every item costs a thumbnail
    /// download when the snapshot is rebuilt.
    public static let maxEntries = 8

    public static func directory(for group: String = appGroup) -> URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)?
            .appendingPathComponent("TopShelf", isDirectory: true)
    }

    public static func imageURL(named name: String, group: String = appGroup) -> URL? {
        directory(for: group)?.appendingPathComponent(name)
    }

    /// The file URL for an entry's artwork, if it has one and it is still on
    /// disk. The extension hands this to tvOS, which reads the file itself —
    /// which is the whole reason the images are downloaded up front.
    public static func imageURL(for entry: TopShelfEntry, group: String = appGroup) -> URL? {
        guard let name = entry.imageName, let url = imageURL(named: name, group: group),
              FileManager.default.fileExists(atPath: url.path) else {
            return nil
        }
        return url
    }

    /// The key the snapshot is stored under in the group's defaults.
    private static let snapshotKey = "topShelfSnapshot"

    /// Where the snapshot lives: the App Group's *defaults*, not a file.
    ///
    /// tvOS gives an app almost no persistent storage — the group container it
    /// hands back sits in a purgeable cache area, so a snapshot written there
    /// can be gone by the time the Home screen asks for it, which looks
    /// exactly like an app that never wrote one. Defaults are small (this is a
    /// few hundred bytes) and are not purged that way. The *images* stay in
    /// the container, because they are the part that must be a file for tvOS
    /// to load, and the part that can be fetched again if it disappears: an
    /// entry whose artwork is gone still shows its title.
    public static func read(group: String = appGroup) -> TopShelfSnapshot? {
        guard let defaults = UserDefaults(suiteName: group),
              let data = defaults.data(forKey: snapshotKey) else {
            return nil
        }
        return try? FlimmCoding.decoder.decode(TopShelfSnapshot.self, from: data)
    }

    public static func write(_ snapshot: TopShelfSnapshot, group: String = appGroup) throws {
        guard let defaults = UserDefaults(suiteName: group) else { throw TopShelfError.noDefaults }
        defaults.set(try FlimmCoding.encoder.encode(snapshot), forKey: snapshotKey)
    }

    /// Removes every image the snapshot does not name. The container is small
    /// and shared; a feed that changes daily would otherwise grow it forever.
    public static func pruneImages(keeping snapshot: TopShelfSnapshot, group: String = appGroup) {
        guard let dir = directory(for: group) else { return }
        let keep = Set(snapshot.entries.compactMap(\.imageName))
        let files = (try? FileManager.default.contentsOfDirectory(atPath: dir.path)) ?? []
        for file in files where !keep.contains(file) {
            try? FileManager.default.removeItem(at: dir.appendingPathComponent(file))
        }
    }

    public static func clear(group: String = appGroup) {
        UserDefaults(suiteName: group)?.removeObject(forKey: snapshotKey)
        guard let dir = directory(for: group) else { return }
        try? FileManager.default.removeItem(at: dir)
    }
}

public enum TopShelfError: Error, LocalizedError {
    /// The App Group entitlement is missing, so there is nowhere both
    /// processes can see.
    case noContainer
    /// The group's defaults would not take the snapshot.
    case noDefaults

    public var errorDescription: String? {
        switch self {
        case .noContainer: "no app group container"
        case .noDefaults: "no app group defaults"
        }
    }
}
