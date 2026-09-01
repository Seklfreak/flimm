import Foundation

/// What a description or a comment is made of, once read.
///
/// YouTube text is plain — no markup — but full of two things a viewer expects
/// to act on: a URL should open, and a timestamp ("at 2:30 the jig slips")
/// should seek, which is the one thing a video's own page can do with it that
/// YouTube's cannot. Everything else is text, kept verbatim, line breaks
/// included.
///
/// The rules live here, once, and mirror the web client's `lib/richText.ts`,
/// so the phone, the iPad and the web link the same things. Views turn the
/// segments into an attributed string; this type has no opinion on colour.
public enum RichTextSegment: Equatable, Sendable {
    case text(String)
    case link(String, URL)
    /// The timestamp as written, and the position it names in seconds.
    case time(String, Double)
}

public enum RichText {
    /// A URL, or a bare `www.` host, up to the next whitespace. Trailing
    /// sentence punctuation belongs to the sentence, not the link ("see
    /// https://x.y."), and so does a closing bracket nothing in the URL opened.
    /// Computed, not stored: `Regex` is not `Sendable`, and building one is
    /// cheap next to the text it runs over.
    private static var link: Regex<Substring> { /(?:https?:\/\/|www\.)[^\s<>"']+/.ignoresCase() }

    /// Splits `text` into segments. Timestamps beyond `duration` (seconds) are
    /// left as text: a "1:30" in a one-minute video is not a place in it.
    /// Without a duration every well-formed timestamp counts.
    public static func segments(_ text: String, duration: Double? = nil) -> [RichTextSegment] {
        var out: [RichTextSegment] = []
        var position = text.startIndex
        func pushText(_ range: Range<String.Index>) {
            if !range.isEmpty { out.append(.text(String(text[range]))) }
        }
        let found = (links(in: text) + times(in: text, duration: duration ?? .infinity))
            .sorted { $0.range.lowerBound < $1.range.lowerBound }
        for match in found {
            // Overlaps happen when a timestamp sits inside a URL (`/1:30`):
            // the link wins, having been found first.
            if match.range.lowerBound < position { continue }
            pushText(position..<match.range.lowerBound)
            out.append(match.segment)
            position = match.range.upperBound
        }
        pushText(position..<text.endIndex)
        return out
    }

    // MARK: - Seeking through a link

    /// The URL a timestamp carries when the text is drawn as one attributed
    /// string: SwiftUI reports a tap on a link through `openURL`, and this is
    /// how a view tells a seek apart from a real link. Not a registered
    /// scheme; it never leaves the app.
    public static let seekScheme = "flimm-seek"

    public static func seekURL(_ seconds: Double) -> URL {
        URL(string: "\(seekScheme):\(Int(seconds))")!
    }

    /// The position a ``seekURL(_:)`` names, or nil for any other URL.
    public static func seekSeconds(_ url: URL) -> Double? {
        guard url.scheme == seekScheme else { return nil }
        // `flimm-seek:90` has no host; the number is the resource specifier.
        let spec = url.absoluteString.dropFirst(seekScheme.count + 1)
        return Double(spec)
    }

    // MARK: - Finding

    private struct Found {
        let range: Range<String.Index>
        let segment: RichTextSegment
    }

    private static func links(in text: String) -> [Found] {
        text.matches(of: link).compactMap { match in
            var raw = Substring(match.output)
            // Strip trailing punctuation, then an unmatched closing bracket,
            // then punctuation again ("(see https://x.y/a).").
            while true {
                var trimmed = raw
                while let last = trimmed.last, ".,;:!?'\"".contains(last) { trimmed.removeLast() }
                if trimmed.last == ")", !trimmed.contains("(") { trimmed.removeLast() }
                if trimmed == raw { break }
                raw = trimmed
            }
            guard !raw.isEmpty else { return nil }
            let string = String(raw)
            let hasScheme = string.lowercased().hasPrefix("http://") || string.lowercased().hasPrefix("https://")
            guard let url = URL(string: hasScheme ? string : "https://" + string) else { return nil }
            return Found(range: raw.startIndex..<raw.endIndex, segment: .link(string, url))
        }
    }

    private static func times(in text: String, duration: Double) -> [Found] {
        // h:mm:ss or m:ss, not glued to a letter, digit or another colon on
        // either side, so "12:30:00" is one timestamp and "v2:30" or "John
        // 3:16b" are not. Swift's regex has no lookbehind; the left side is
        // checked by hand below.
        let time = /(?:(\d{1,2}):)?(\d{1,2}):(\d{2})(?![\w:])/
        return text.matches(of: time).compactMap { match in
            let range = match.range
            if range.lowerBound > text.startIndex {
                let before = text[text.index(before: range.lowerBound)]
                if before == ":" || before.isLetter || before.isNumber || before == "_" { return nil }
            }
            let (raw, hours, minutes, seconds) = match.output
            guard let minute = Int(minutes), let second = Int(seconds), second <= 59 else { return nil }
            var total = Double(minute * 60 + second)
            if let hours {
                guard let hour = Int(hours), minute <= 59 else { return nil }
                total += Double(hour * 3600)
            }
            guard total <= duration else { return nil }
            return Found(range: range, segment: .time(String(raw), total))
        }
    }
}
