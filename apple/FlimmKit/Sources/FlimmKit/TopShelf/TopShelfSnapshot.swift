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
    /// An absolute URL tvOS can fetch the artwork from, carrying its own
    /// media token — the system draws the shelf in a process with no session
    /// of ours, and **the App Group container is not writable on tvOS**, so a
    /// downloaded file is not an option. Nil when no token could be got; the
    /// entry still shows, with tvOS's own placeholder.
    public let imageURL: String?
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
        case imageURL = "imageUrl"
        case title, channel, progress, duration
    }

    public init(
        videoID: String,
        title: String,
        channel: String,
        imageURL: String?,
        progress: Double,
        duration: Double
    ) {
        self.videoID = videoID
        self.title = title
        self.channel = channel
        self.imageURL = imageURL
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

/// Where the snapshot lives: the App Group's shared defaults.
///
/// **Not a file.** On tvOS an App Group shares *preferences*, not a writable
/// directory — `containerURL(forSecurityApplicationGroupIdentifier:)` returns a
/// path the app cannot write to, and trying earned "You don't have permission
/// to save the file". The simulator does not enforce that, which is exactly
/// how it shipped. Artwork therefore travels as a URL tvOS fetches itself
/// rather than as a file we place for it.
public enum TopShelfStore {
    /// The App Group. Both the app and the extension carry it as an
    /// entitlement; without it there are no shared defaults and the shelf
    /// simply stays empty.
    public static let appGroup = "group.dev.winktech.flimm"

    /// The URL scheme the top shelf's actions use to open a video. tvOS has no
    /// browser and no OIDC redirect, so this is the app's only URL type.
    public static let urlScheme = "dev.winktech.flimm.tv"

    /// How many videos the row holds. The top shelf scrolls, but nobody
    /// scrolls a Home screen shelf far.
    public static let maxEntries = 8

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

    public static func clear(group: String = appGroup) {
        UserDefaults(suiteName: group)?.removeObject(forKey: snapshotKey)
    }
}

public enum TopShelfError: Error, LocalizedError {
    /// The group's defaults would not take the snapshot — the App Group
    /// entitlement is missing.
    case noDefaults

    public var errorDescription: String? {
        switch self {
        case .noDefaults: "no app group defaults"
        }
    }
}
