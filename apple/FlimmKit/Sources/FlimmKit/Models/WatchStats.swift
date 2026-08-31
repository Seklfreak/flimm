import Foundation

/// `GET /stats` — what a viewer's history adds up to.
///
/// Read the server's note in `docs/api.md` ("Watch stats") before showing any
/// of this: the table behind it holds one row per video, so `seconds` is the
/// summed *furthest point reached*, not a stopwatch, and the hour and weekday
/// breakdowns are when a video was first **started**. Every client says so on
/// screen, because a number nobody qualifies is a number people trust.
public struct WatchStats: Codable, Sendable, Hashable {
    public let started: Int
    public let finished: Int
    public let seconds: Double
    public let since: Date?
    public let range: StatsRange
    /// The timezone the breakdowns were computed in — the one this client
    /// asked for, echoed back so the screen can name it.
    public let zone: String
    public let topChannels: [StatsChannel]
    /// 24 counts, midnight first.
    public let byHour: [Int]
    /// 7 counts, Monday first.
    public let byWeekday: [Int]
    public let byMonth: [StatsMonth]

    /// Videos finished as a share of those started, or nil when nothing has
    /// been watched — a rate over no videos is not 0%, it is nothing.
    public var finishRate: Double? {
        started > 0 ? Double(finished) / Double(started) : nil
    }

    public init(
        started: Int = 0,
        finished: Int = 0,
        seconds: Double = 0,
        since: Date? = nil,
        range: StatsRange = .all,
        zone: String = "UTC",
        topChannels: [StatsChannel] = [],
        byHour: [Int] = Array(repeating: 0, count: 24),
        byWeekday: [Int] = Array(repeating: 0, count: 7),
        byMonth: [StatsMonth] = []
    ) {
        self.started = started
        self.finished = finished
        self.seconds = seconds
        self.since = since
        self.range = range
        self.zone = zone
        self.topChannels = topChannels
        self.byHour = byHour
        self.byWeekday = byWeekday
        self.byMonth = byMonth
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        started = try c.decode(.started, or: 0)
        finished = try c.decode(.finished, or: 0)
        seconds = try c.decode(.seconds, or: 0)
        since = try c.decodeIfPresent(Date.self, forKey: .since)
        range = (try? c.decode(StatsRange.self, forKey: .range)) ?? .all
        zone = try c.decode(.zone, or: "UTC")
        topChannels = try c.decode(.topChannels, or: [])
        byMonth = try c.decode(.byMonth, or: [])
        // Padded rather than trusted: a client draws a fixed number of columns
        // and must not have to decide what a short array means.
        byHour = WatchStats.padded(try c.decode(.byHour, or: []), to: 24)
        byWeekday = WatchStats.padded(try c.decode(.byWeekday, or: []), to: 7)
    }

    private static func padded(_ values: [Int], to count: Int) -> [Int] {
        guard values.count != count else { return values }
        var out = Array(values.prefix(count))
        out.append(contentsOf: Array(repeating: 0, count: max(0, count - out.count)))
        return out
    }
}

/// What a stats request covers. Calendar windows, not rolling ones.
public enum StatsRange: String, Codable, Sendable, Hashable, CaseIterable {
    case all, year, month

    public var label: String {
        switch self {
        case .all: "All time"
        case .year: "This year"
        case .month: "This month"
        }
    }
}

public struct StatsChannel: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let videos: Int
    public let seconds: Double

    public init(id: String, name: String, videos: Int, seconds: Double) {
        self.id = id
        self.name = name
        self.videos = videos
        self.seconds = seconds
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(.id, or: "")
        name = try c.decode(.name, or: "")
        videos = try c.decode(.videos, or: 0)
        seconds = try c.decode(.seconds, or: 0)
    }
}

/// One calendar month, `YYYY-MM` in the requested zone.
public struct StatsMonth: Codable, Sendable, Hashable, Identifiable {
    public let month: String
    public let videos: Int
    public let seconds: Double

    public var id: String { month }

    public init(month: String, videos: Int, seconds: Double) {
        self.month = month
        self.videos = videos
        self.seconds = seconds
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        month = try c.decode(.month, or: "")
        videos = try c.decode(.videos, or: 0)
        seconds = try c.decode(.seconds, or: 0)
    }
}
