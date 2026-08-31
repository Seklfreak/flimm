import Foundation

/// The `{ id, name }` stub a channel carries for each feed it belongs to.
public struct FeedRef: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String

    public init(id: String, name: String) {
        self.id = id
        self.name = name
    }
}

public struct ChannelSummary: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let thumbUrl: String
    public let bannerUrl: String
    public let videoCount: Int
    public let unseenCount: Int
    public let lastUpload: Date?
    public let subscribed: Bool
    /// Pinned to the sidebar — Flimm's own per-user state, like a playlist pin.
    public let pinned: Bool
    /// The feeds this channel is in — backs the "In feeds:" control.
    public let feeds: [FeedRef]

    public init(
        id: String,
        name: String,
        thumbUrl: String = "",
        bannerUrl: String = "",
        videoCount: Int = 0,
        unseenCount: Int = 0,
        lastUpload: Date? = nil,
        subscribed: Bool = true,
        pinned: Bool = false,
        feeds: [FeedRef] = []
    ) {
        self.id = id
        self.name = name
        self.thumbUrl = thumbUrl
        self.bannerUrl = bannerUrl
        self.videoCount = videoCount
        self.unseenCount = unseenCount
        self.lastUpload = lastUpload
        self.subscribed = subscribed
        self.pinned = pinned
        self.feeds = feeds
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        thumbUrl = try c.decode(.thumbUrl, or: "")
        bannerUrl = try c.decode(.bannerUrl, or: "")
        videoCount = try c.decode(.videoCount, or: 0)
        unseenCount = try c.decode(.unseenCount, or: 0)
        lastUpload = try c.decodeIfPresent(Date.self, forKey: .lastUpload)
        subscribed = try c.decode(.subscribed, or: true)
        pinned = try c.decode(.pinned, or: false)
        feeds = try c.decode(.feeds, or: [])
    }
}

/// `GET /channels/{id}` — a ``ChannelSummary`` with the description.
public struct Channel: Codable, Sendable, Hashable, Identifiable {
    public let summary: ChannelSummary
    public let description: String

    public var id: String { summary.id }
    public var name: String { summary.name }

    public init(summary: ChannelSummary, description: String) {
        self.summary = summary
        self.description = description
    }

    private enum CodingKeys: String, CodingKey {
        case description
    }

    public init(from decoder: any Decoder) throws {
        summary = try ChannelSummary(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        description = try c.decode(.description, or: "")
    }

    public func encode(to encoder: any Encoder) throws {
        try summary.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(description, forKey: .description)
    }
}

/// A channel search hit: the summary plus how many things matched.
public struct ChannelMatch: Codable, Sendable, Hashable, Identifiable {
    public let channel: ChannelSummary
    public let matchCount: Int

    public var id: String { channel.id }

    private enum CodingKeys: String, CodingKey {
        case matchCount
    }

    public init(channel: ChannelSummary, matchCount: Int) {
        self.channel = channel
        self.matchCount = matchCount
    }

    public init(from decoder: any Decoder) throws {
        channel = try ChannelSummary(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        matchCount = try c.decode(.matchCount, or: 0)
    }

    public func encode(to encoder: any Encoder) throws {
        try channel.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(matchCount, forKey: .matchCount)
    }
}
