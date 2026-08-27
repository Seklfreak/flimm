import Foundation

public enum ChaptersSource: String, Codable, Sendable {
    /// Read from the container's Nero `chpl` box (or the QuickTime chapter
    /// track) — YouTube's own chapters, embedded by yt-dlp at download time.
    case embedded
    /// Crowd-sourced chapter names submitted to SponsorBlock. Used when the
    /// file carries none of its own; hand-written names beat the description
    /// heuristic.
    case sponsorblock
    /// Parsed from timestamp lines in the description.
    case description
    case none
}

/// Times are seconds. `end` is the next chapter's `start`; the last chapter
/// ends at the video duration. Titles are trimmed and never empty.
public struct Chapter: Codable, Sendable, Hashable, Identifiable {
    public let start: Double
    public let end: Double
    public let title: String

    public var id: Double { start }

    public init(start: Double, end: Double, title: String) {
        self.start = start
        self.end = end
        self.title = title
    }
}

/// `GET /videos/{id}/chapters`. Roughly a third of videos have none, so an
/// empty list means "no chapter UI" — never an error.
public struct Chapters: Codable, Sendable, Hashable {
    public let source: ChaptersSource
    public let chapters: [Chapter]

    public var isEmpty: Bool { chapters.isEmpty }

    public init(source: ChaptersSource = .none, chapters: [Chapter] = []) {
        self.source = source
        self.chapters = chapters
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        source = try c.decode(.source, or: ChaptersSource.none)
        chapters = try c.decode(.chapters, or: [])
    }
}
