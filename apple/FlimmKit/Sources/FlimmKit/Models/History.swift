import Foundation

public enum HistoryState: String, Codable, Sendable {
    case inProgress = "in_progress"
    case seen
}

public enum HistoryFilter: String, Codable, Sendable, CaseIterable {
    case all
    case inProgress = "in_progress"
    case seen
}

/// One row of `GET /history`. Entries below `MIN_PLAY_SECONDS` that never
/// completed are filtered out server-side, so a client never sees them.
public struct HistoryEntry: Codable, Sendable, Hashable, Identifiable {
    /// The entry id, *not* the video id — `DELETE /history/{id}` takes this.
    public let id: String
    public let video: VideoSummary
    public let playedAt: Date?
    public let state: HistoryState
    /// The series the video belongs to through a feed's playlist source —
    /// the resume context when set, so up next is the next episode.
    public let playlistId: String?
    /// The feed holding the video's channel — the resume context when no
    /// series claims it. Nil when no feed holds the video at all.
    public let feed: FeedRef?

    /// The context a tap on this entry should play with: series first, else
    /// the feed.
    public var playbackContext: PlaybackContext {
        if let playlistId { return .playlist(playlistId) }
        return feed.map { .feed($0.id) } ?? .none
    }

    public init(id: String, video: VideoSummary, playedAt: Date?, state: HistoryState, playlistId: String? = nil, feed: FeedRef? = nil) {
        self.id = id
        self.video = video
        self.playedAt = playedAt
        self.state = state
        self.playlistId = playlistId
        self.feed = feed
    }
}
