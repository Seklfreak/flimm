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

    public init(id: String, video: VideoSummary, playedAt: Date?, state: HistoryState) {
        self.id = id
        self.video = video
        self.playedAt = playedAt
        self.state = state
    }
}
