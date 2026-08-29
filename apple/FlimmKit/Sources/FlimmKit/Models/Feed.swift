import Foundation

public enum FeedSort: String, Codable, Sendable, CaseIterable {
    case newest
    case oldest
    case shortest
    case longest
}

/// `view=` on `GET /feeds/{id}/videos`. Omitting it lets the feed's own
/// `hideSeen` decide, which is what the sidebar does.
///
/// There is no "in progress" case: the unseen view opens with the videos the
/// viewer is part-way through, so a filter for them would list what is already
/// at the top of the list it filters.
public enum FeedView: String, Codable, Sendable, CaseIterable {
    case unseen
    case all
}

public struct Feed: Codable, Sendable, Hashable, Identifiable {
    /// The built-in, read-only feed over every channel. Always sorts last.
    public static let everythingID = "everything"

    public let id: String
    public let name: String
    /// Empty for ``everythingID``, which means "all channels".
    public let channelIds: [String]
    public let channelCount: Int
    public let unseenCount: Int
    public let sort: FeedSort
    public let hideSeen: Bool
    public let includeShorts: Bool
    public let subtitlesOnly: Bool
    /// At most one feed is pinned; it is the one the app opens on.
    public let pinned: Bool
    public let position: Int
    public let createdAt: Date?
    public let updatedAt: Date?

    /// "Everything" is read-only except for sort/hideSeen/includeShorts, which
    /// live in prefs rather than on the feed.
    public var isEverything: Bool { id == Feed.everythingID }

    public init(
        id: String,
        name: String,
        channelIds: [String] = [],
        channelCount: Int = 0,
        unseenCount: Int = 0,
        sort: FeedSort = .newest,
        hideSeen: Bool = true,
        includeShorts: Bool = false,
        subtitlesOnly: Bool = false,
        pinned: Bool = false,
        position: Int = 0,
        createdAt: Date? = nil,
        updatedAt: Date? = nil
    ) {
        self.id = id
        self.name = name
        self.channelIds = channelIds
        self.channelCount = channelCount
        self.unseenCount = unseenCount
        self.sort = sort
        self.hideSeen = hideSeen
        self.includeShorts = includeShorts
        self.subtitlesOnly = subtitlesOnly
        self.pinned = pinned
        self.position = position
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        channelIds = try c.decode(.channelIds, or: [])
        channelCount = try c.decode(.channelCount, or: 0)
        unseenCount = try c.decode(.unseenCount, or: 0)
        sort = try c.decode(.sort, or: FeedSort.newest)
        hideSeen = try c.decode(.hideSeen, or: true)
        includeShorts = try c.decode(.includeShorts, or: false)
        subtitlesOnly = try c.decode(.subtitlesOnly, or: false)
        pinned = try c.decode(.pinned, or: false)
        position = try c.decode(.position, or: 0)
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt)
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }
}

/// Body for `POST /feeds` and `PUT /feeds/{id}`. `PUT` is a full update, so
/// the same shape serves both.
public struct FeedInput: Codable, Sendable, Hashable {
    public var name: String
    public var channelIds: [String]
    public var sort: FeedSort
    public var hideSeen: Bool
    public var includeShorts: Bool
    public var subtitlesOnly: Bool
    /// `true` unpins every other feed server-side.
    public var pinned: Bool

    public init(
        name: String,
        channelIds: [String] = [],
        sort: FeedSort = .newest,
        hideSeen: Bool = true,
        includeShorts: Bool = false,
        subtitlesOnly: Bool = false,
        pinned: Bool = false
    ) {
        self.name = name
        self.channelIds = channelIds
        self.sort = sort
        self.hideSeen = hideSeen
        self.includeShorts = includeShorts
        self.subtitlesOnly = subtitlesOnly
        self.pinned = pinned
    }

    public init(feed: Feed) {
        self.init(
            name: feed.name,
            channelIds: feed.channelIds,
            sort: feed.sort,
            hideSeen: feed.hideSeen,
            includeShorts: feed.includeShorts,
            subtitlesOnly: feed.subtitlesOnly,
            pinned: feed.pinned
        )
    }
}
