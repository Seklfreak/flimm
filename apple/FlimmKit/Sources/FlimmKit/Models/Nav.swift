import Foundation

/// `GET /videos/{id}/nav` — where the video sits in the context list.
///
/// `previous`/`next` are `nil` at the ends (there is no wrap-around) and
/// ``index`` is `-1` when the video is not in the list at all — opened without
/// a context, or dropped out of a hide-seen feed since. Clients hide the step
/// controls in that case and disable a single button at the ends.
public struct Nav: Codable, Sendable, Hashable {
    public let index: Int
    public let total: Int
    public let previous: VideoSummary?
    public let next: VideoSummary?
    /// Head of the list — the entry point for a shuffled run, so a client
    /// never derives the shuffled order itself.
    public let first: VideoSummary?

    /// `true` when the video is not part of the context list.
    public var isDetached: Bool { index < 0 }

    public init(index: Int, total: Int, previous: VideoSummary?, next: VideoSummary?, first: VideoSummary?) {
        self.index = index
        self.total = total
        self.previous = previous
        self.next = next
        self.first = first
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        index = try c.decode(.index, or: -1)
        total = try c.decode(.total, or: 0)
        previous = try c.decodeIfPresent(VideoSummary.self, forKey: .previous)
        next = try c.decodeIfPresent(VideoSummary.self, forKey: .next)
        first = try c.decodeIfPresent(VideoSummary.self, forKey: .first)
    }
}
