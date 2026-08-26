import Foundation

public enum PlaylistKind: String, Codable, Sendable, CaseIterable {
    case custom
    case channel
}

/// The channel a `kind == .channel` playlist belongs to.
public struct PlaylistChannelRef: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String

    public init(id: String, name: String) {
        self.id = id
        self.name = name
    }
}

public struct PlaylistSummary: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let kind: PlaylistKind
    public let channel: PlaylistChannelRef?
    public let thumbUrl: String
    public let videoCount: Int
    public let totalDuration: Double
    public let seenCount: Int
    public let inProgressCount: Int
    public let progress: Double
    /// First in-progress video, else the first unseen one. `nil` for a music
    /// playlist, which records no watch state at all.
    public let resumeVideoId: String?
    /// Shown in the client's sidebar. Flimm's own state, per user.
    public let pinned: Bool
    /// A music playlist: audio-only playback and no watch state, recorded or
    /// reported. Seeds `audio=1` on every link into it.
    public let music: Bool

    public init(
        id: String,
        name: String,
        kind: PlaylistKind = .custom,
        channel: PlaylistChannelRef? = nil,
        thumbUrl: String = "",
        videoCount: Int = 0,
        totalDuration: Double = 0,
        seenCount: Int = 0,
        inProgressCount: Int = 0,
        progress: Double = 0,
        resumeVideoId: String? = nil,
        pinned: Bool = false,
        music: Bool = false
    ) {
        self.id = id
        self.name = name
        self.kind = kind
        self.channel = channel
        self.thumbUrl = thumbUrl
        self.videoCount = videoCount
        self.totalDuration = totalDuration
        self.seenCount = seenCount
        self.inProgressCount = inProgressCount
        self.progress = progress
        self.resumeVideoId = resumeVideoId
        self.pinned = pinned
        self.music = music
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        kind = try c.decode(.kind, or: PlaylistKind.custom)
        channel = try c.decodeIfPresent(PlaylistChannelRef.self, forKey: .channel)
        thumbUrl = try c.decode(.thumbUrl, or: "")
        videoCount = try c.decode(.videoCount, or: 0)
        totalDuration = try c.decode(.totalDuration, or: 0)
        seenCount = try c.decode(.seenCount, or: 0)
        inProgressCount = try c.decode(.inProgressCount, or: 0)
        progress = try c.decode(.progress, or: 0)
        resumeVideoId = try c.decodeIfPresent(String.self, forKey: .resumeVideoId)
        pinned = try c.decode(.pinned, or: false)
        music = try c.decode(.music, or: false)
    }
}

public struct PlaylistItem: Codable, Sendable, Hashable, Identifiable {
    public let position: Int
    public let video: VideoSummary

    public var id: String { video.id }

    public init(position: Int, video: VideoSummary) {
        self.position = position
        self.video = video
    }
}

/// `GET /playlists/{id}` — the summary plus its ordered items.
public struct Playlist: Codable, Sendable, Hashable, Identifiable {
    public let summary: PlaylistSummary
    public let items: [PlaylistItem]

    public var id: String { summary.id }
    public var name: String { summary.name }
    public var music: Bool { summary.music }

    public init(summary: PlaylistSummary, items: [PlaylistItem]) {
        self.summary = summary
        self.items = items
    }

    private enum CodingKeys: String, CodingKey {
        case items
    }

    public init(from decoder: any Decoder) throws {
        summary = try PlaylistSummary(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        items = try c.decode(.items, or: [])
    }

    public func encode(to encoder: any Encoder) throws {
        try summary.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(items, forKey: .items)
    }
}

/// A playlist search hit.
public struct PlaylistMatch: Codable, Sendable, Hashable, Identifiable {
    public let playlist: PlaylistSummary
    public let matchCount: Int

    public var id: String { playlist.id }

    private enum CodingKeys: String, CodingKey {
        case matchCount
    }

    public init(playlist: PlaylistSummary, matchCount: Int) {
        self.playlist = playlist
        self.matchCount = matchCount
    }

    public init(from decoder: any Decoder) throws {
        playlist = try PlaylistSummary(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        matchCount = try c.decode(.matchCount, or: 0)
    }

    public func encode(to encoder: any Encoder) throws {
        try playlist.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(matchCount, forKey: .matchCount)
    }
}

/// `POST /playlists/{id}/videos` — the TubeArchivist custom-playlist actions.
public enum PlaylistAction: String, Codable, Sendable, CaseIterable {
    case add
    case remove
    case up
    case down
    case top
    case bottom
}
