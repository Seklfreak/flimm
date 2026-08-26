import Foundation

/// Presentation helpers shared by every screen.
///
/// They deliberately mirror `frontend/src/lib/format.ts` so the web and native
/// clients phrase the same things the same way ("seen yesterday", "CC EN",
/// "1:02:03").
enum Fmt {
    /// Seconds → `9:21` / `1:02:03`.
    static func duration(_ seconds: Double) -> String {
        let total = max(0, Int(seconds.rounded(.down)))
        let hours = total / 3600
        let minutes = (total % 3600) / 60
        let secs = total % 60
        if hours > 0 {
            return String(format: "%d:%02d:%02d", hours, minutes, secs)
        }
        return String(format: "%d:%02d", minutes, secs)
    }

    /// A total → `4 h 12 min` / `48 min`.
    static func durationLong(_ seconds: Double) -> String {
        let total = max(0, Int(seconds.rounded()))
        let hours = total / 3600
        let minutes = Int((Double(total % 3600) / 60).rounded())
        if hours == 0 { return "\(minutes) min" }
        return minutes == 0 ? "\(hours) h" : "\(hours) h \(minutes) min"
    }

    /// `today` / `yesterday` / `3 days ago` / `last week` / `Mar 3`.
    static func relativeDay(_ date: Date?, now: Date = Date()) -> String {
        guard let date else { return "unknown" }
        let days = dayDiff(date, now)
        switch days {
        case ..<1: return "today"
        case 1: return "yesterday"
        case 2..<7: return "\(days) days ago"
        case 7..<14: return "last week"
        case 14..<30: return "\(days / 7) weeks ago"
        default: return shortDate(date, now: now)
        }
    }

    /// `seen today` / `seen Sunday` / `seen Mar 3`.
    static func seenLabel(_ date: Date?, now: Date = Date()) -> String {
        guard let date else { return "seen" }
        let days = dayDiff(date, now)
        switch days {
        case ..<1: return "seen today"
        case 1: return "seen yesterday"
        case 2..<7: return "seen \(date.formatted(.dateTime.weekday(.wide)))"
        default: return "seen \(relativeDay(date, now: now))"
        }
    }

    /// History section heading: `Today` / `Yesterday` / `Sunday` / `Mon, Aug 18`.
    static func dayHeading(_ date: Date?, now: Date = Date()) -> String {
        guard let date else { return "Earlier" }
        let days = dayDiff(date, now)
        switch days {
        case ..<1: return "Today"
        case 1: return "Yesterday"
        case 2..<7: return date.formatted(.dateTime.weekday(.wide))
        default: return date.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day())
        }
    }

    /// `CC EN` / `CC EN, DE` / `CC auto` / `no subtitles`.
    static func ccLabel(langs: [String], hasAuto: Bool) -> String {
        if !langs.isEmpty { return "CC " + langs.map { $0.uppercased() }.joined(separator: ", ") }
        return hasAuto ? "CC auto" : "no subtitles"
    }

    /// Counts at or above Elasticsearch's 10 000 total-hits cap show as `10,000+`.
    static func count(_ number: Int) -> String {
        number >= 10000 ? "10,000+" : String(number)
    }

    static func plural(_ number: Int, _ one: String, _ many: String? = nil) -> String {
        "\(count(number)) \(number == 1 ? one : (many ?? one + "s"))"
    }

    /// `1×` / `1.25×`.
    static func speed(_ rate: Double) -> String {
        let text = rate == rate.rounded() ? String(Int(rate)) : String(format: "%g", rate)
        return "\(text)×"
    }

    /// A language code as a display name, falling back to the uppercased code.
    static func langName(_ code: String) -> String {
        Locale.current.localizedString(forLanguageCode: code) ?? code.uppercased()
    }

    /// A playlist's remaining-unseen count, clamped so a transiently high
    /// `seen_count` can never show a negative badge.
    static func remainingUnseen(videoCount: Int, seenCount: Int) -> Int {
        max(0, videoCount - seenCount)
    }

    // MARK: - Internals

    private static func dayDiff(_ date: Date, _ now: Date) -> Int {
        let cal = Calendar.current
        let from = cal.startOfDay(for: date)
        let to = cal.startOfDay(for: now)
        return cal.dateComponents([.day], from: from, to: to).day ?? 0
    }

    private static func shortDate(_ date: Date, now: Date) -> String {
        let cal = Calendar.current
        if cal.component(.year, from: date) == cal.component(.year, from: now) {
            return date.formatted(.dateTime.month(.abbreviated).day())
        }
        return date.formatted(.dateTime.month(.abbreviated).day().year())
    }
}
