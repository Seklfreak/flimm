import Foundation

public struct SubtitleCue: Sendable, Hashable {
    public let start: Double
    public let end: Double
    public let text: String

    public init(start: Double, end: Double, text: String) {
        self.start = start
        self.end = end
        self.text = text
    }
}

/// A minimal WebVTT reader.
///
/// The tracks live behind `/media/subtitles/{id}/{lang}.vtt`, which needs the
/// bearer header, and `AVPlayer` cannot side-load an external track with custom
/// headers. Parsing the cues and drawing them ourselves is both simpler and
/// gives the size preference somewhere to apply.
public enum WebVTT {
    public static func parse(_ source: String) -> [SubtitleCue] {
        var cues: [SubtitleCue] = []
        var pendingRange: (start: Double, end: Double)?
        var lines: [String] = []

        func flush() {
            defer {
                pendingRange = nil
                lines = []
            }
            guard let range = pendingRange else { return }
            let text = lines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { return }
            cues.append(SubtitleCue(start: range.start, end: range.end, text: strippingTags(text)))
        }

        for rawLine in source.replacingOccurrences(of: "\r\n", with: "\n").split(separator: "\n", omittingEmptySubsequences: false) {
            let line = String(rawLine)
            if line.trimmingCharacters(in: .whitespaces).isEmpty {
                flush()
                continue
            }
            if line.contains("-->") {
                flush()
                pendingRange = timings(from: line)
                continue
            }
            if pendingRange != nil { lines.append(line) }
        }
        flush()
        return cues.sorted { $0.start < $1.start }
    }

    /// The cue covering `time`, if any. Cues are sorted, so a linear scan from a
    /// remembered index would be faster — but a track is a few thousand entries
    /// at most and this runs four times a second.
    public static func cue(at time: Double, in cues: [SubtitleCue]) -> SubtitleCue? {
        cues.last { $0.start <= time && time < $0.end }
    }

    // MARK: - Internals

    private static func timings(from line: String) -> (start: Double, end: Double)? {
        let parts = line.components(separatedBy: "-->")
        guard parts.count >= 2,
              let start = seconds(from: parts[0]),
              let end = seconds(from: parts[1].components(separatedBy: .whitespaces).first { !$0.isEmpty } ?? "") else {
            return nil
        }
        return (start, end)
    }

    /// `00:01:02.500`, `01:02.500` or `01:02,500`.
    public static func seconds(from raw: String) -> Double? {
        let text = raw.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: ",", with: ".")
        guard !text.isEmpty else { return nil }
        let parts = text.components(separatedBy: ":")
        guard parts.count >= 2, parts.count <= 3 else { return nil }
        var total: Double = 0
        for part in parts {
            guard let value = Double(part) else { return nil }
            total = total * 60 + value
        }
        return total
    }

    /// WebVTT allows `<v Speaker>`, `<c.classname>`, `<00:00:01.000>` and the
    /// like inside cue text; none of them mean anything to a plain `Text`.
    private static func strippingTags(_ text: String) -> String {
        var output = ""
        var insideTag = false
        for character in text {
            switch character {
            case "<": insideTag = true
            case ">": insideTag = false
            default: if !insideTag { output.append(character) }
            }
        }
        return decodingEntities(output)
    }

    /// The character references the format requires.
    ///
    /// A cue cannot contain a bare `&` or `<` — the spec makes them escapes —
    /// so any caption with an ampersand in it arrives as `&amp;` and was drawn
    /// on screen exactly like that. A browser never had this problem: the web
    /// client hands the file to a native `<track>`, which parses it per spec.
    /// These players parse it themselves, because `AVPlayer` cannot side-load
    /// a track that needs the bearer header, and parsing it themselves means
    /// owning this too.
    ///
    /// `&amp;` is decoded last, or `&amp;lt;` — the way a caption writes a
    /// literal `&lt;` — would come out as `<`.
    private static func decodingEntities(_ text: String) -> String {
        guard text.contains("&") else { return text }
        var output = text
        for (entity, replacement) in [
            ("&lt;", "<"), ("&gt;", ">"), ("&quot;", "\""), ("&apos;", "'"), ("&#39;", "'"),
            ("&nbsp;", "\u{00A0}"), ("&lrm;", "\u{200E}"), ("&rlm;", "\u{200F}"),
            ("&amp;", "&"),
        ] {
            output = output.replacingOccurrences(of: entity, with: replacement)
        }
        return output
    }
}

/// Fetches a track with the bearer header and parses it.
public enum SubtitleLoader {
    public static func load(track: SubtitleTrack, client: APIClient) async -> [SubtitleCue] {
        guard let url = client.mediaURL(track.url) else { return [] }
        guard let headers = try? await client.mediaHeaders() else { return [] }
        var request = URLRequest(url: url)
        for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode),
              let text = String(data: data, encoding: .utf8) else {
            return []
        }
        return WebVTT.parse(text)
    }

    /// The track to show by default: the preferred language if it is archived,
    /// English otherwise, and nothing when the preference is "off".
    public static func pick(from tracks: [SubtitleTrack], preferred lang: String) -> SubtitleTrack? {
        guard lang != Prefs.subtitlesOff, !tracks.isEmpty else { return nil }
        if let exact = tracks.first(where: { $0.lang == lang && $0.source == .user }) { return exact }
        if let any = tracks.first(where: { $0.lang == lang }) { return any }
        if let english = tracks.first(where: { $0.lang == "en" }) { return english }
        return nil
    }
}
