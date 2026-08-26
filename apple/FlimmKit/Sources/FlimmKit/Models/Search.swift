import Foundation

public enum SearchScope: String, Codable, Sendable, CaseIterable {
    case all
    case titles
    case subtitles
    case channels
    case playlists
}

/// One subtitle line that matched, with the timestamp to seek to. This is the
/// only place `t=` is used in a player link — resume never needs it.
public struct SubtitleHit: Codable, Sendable, Hashable {
    public let lang: String
    public let start: Double
    public let end: Double
    public let text: String

    public init(lang: String, start: Double, end: Double, text: String) {
        self.lang = lang
        self.start = start
        self.end = end
        self.text = text
    }
}

public struct VideoMatch: Codable, Sendable, Hashable, Identifiable {
    public let video: VideoSummary
    public let subtitleHits: [SubtitleHit]

    public var id: String { video.id }

    public init(video: VideoSummary, subtitleHits: [SubtitleHit] = []) {
        self.video = video
        self.subtitleHits = subtitleHits
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        video = try c.decode(VideoSummary.self, forKey: .video)
        subtitleHits = try c.decode(.subtitleHits, or: [])
    }
}

/// One result section: a total and the items that fit on this response.
public struct SearchSection<Item: Codable & Sendable & Hashable>: Codable, Sendable, Hashable {
    public let total: Int
    public let items: [Item]

    public var isEmpty: Bool { items.isEmpty }

    public init(total: Int = 0, items: [Item] = []) {
        self.total = total
        self.items = items
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        total = try c.decode(.total, or: 0)
        items = try c.decode(.items, or: [])
    }
}

/// `GET /search`.
public struct SearchResults: Codable, Sendable, Hashable {
    public let tookMs: Int
    public let videos: SearchSection<VideoMatch>
    public let channels: SearchSection<ChannelMatch>
    public let playlists: SearchSection<PlaylistMatch>

    public var isEmpty: Bool { videos.isEmpty && channels.isEmpty && playlists.isEmpty }

    public init(
        tookMs: Int = 0,
        videos: SearchSection<VideoMatch> = .init(),
        channels: SearchSection<ChannelMatch> = .init(),
        playlists: SearchSection<PlaylistMatch> = .init()
    ) {
        self.tookMs = tookMs
        self.videos = videos
        self.channels = channels
        self.playlists = playlists
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tookMs = try c.decode(.tookMs, or: 0)
        videos = try c.decode(.videos, or: SearchSection<VideoMatch>())
        channels = try c.decode(.channels, or: SearchSection<ChannelMatch>())
        playlists = try c.decode(.playlists, or: SearchSection<PlaylistMatch>())
    }
}
