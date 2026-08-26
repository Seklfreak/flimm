import Foundation

/// Chapter lookup and the scrubber's marker offsets.
///
/// Mirrors `frontend/src/player/chapterMath.ts`. Roughly a third of videos have
/// no chapters at all, so every function here has to behave sensibly on an
/// empty list — an empty list is "no chapter UI", never an error.
public enum ChapterMath {
    /// Index of the chapter containing `time`, or `-1` before the first one.
    public static func index(of time: Double, in chapters: [Chapter]) -> Int {
        var found = -1
        for (position, chapter) in chapters.enumerated() {
            if chapter.start <= time { found = position } else { break }
        }
        return found
    }

    /// How far into a chapter `[` still counts as "go to the previous one".
    public static let previousChapterThreshold: Double = 3

    /// `]` — the start of the next chapter, or `nil` when `time` is already in
    /// or past the last one.
    public static func nextStart(after time: Double, in chapters: [Chapter]) -> Double? {
        chapters.first { $0.start > time }?.start
    }

    /// `[` — like most players: more than `threshold` seconds into the current
    /// chapter jumps back to its own start, and within that window it jumps to
    /// the previous chapter instead. `nil` when there is nowhere earlier to go.
    public static func previousStart(
        before time: Double,
        in chapters: [Chapter],
        threshold: Double = previousChapterThreshold
    ) -> Double? {
        let current = index(of: time, in: chapters)
        guard current >= 0 else { return nil }
        let chapter = chapters[current]
        if time - chapter.start > threshold { return chapter.start }
        guard current > 0 else { return nil }
        return chapters[current - 1].start
    }

    /// Fractions of the whole bar for the boundary ticks. The first chapter
    /// always starts at 0, and a tick on the very edge of the bar is invisible.
    public static func markerFractions(_ chapters: [Chapter], duration: Double) -> [Double] {
        guard duration > 0, chapters.count >= 2 else { return [] }
        return chapters.dropFirst().map { min(max($0.start / duration, 0), 1) }
    }
}

/// One tinted band on the scrubber, in fractions of the whole bar.
public struct SponsorRange: Sendable, Hashable {
    public let category: String
    public let start: Double
    public let width: Double

    public init(category: String, start: Double, width: Double) {
        self.category = category
        self.start = start
        self.width = width
    }
}

/// SponsorBlock: what to tint, what to skip, and what to call it.
public enum SponsorRules {
    /// Only these are skipped automatically; intro/outro/music_offtopic and the
    /// rest are tinted on the scrubber but left alone. Matches the web client.
    public static let autoSkip: Set<String> = ["sponsor", "selfpromo", "interaction"]

    private static let labels: [String: String] = [
        "sponsor": "Sponsor",
        "selfpromo": "Self-promo",
        "interaction": "Interaction reminder",
        "intro": "Intro",
        "outro": "Outro",
        "preview": "Preview/recap",
        "music_offtopic": "Non-music section",
        "filler": "Filler tangent",
        "poi_highlight": "Highlight",
        "exclusive_access": "Exclusive access"
    ]

    public static func label(_ category: String) -> String {
        labels[category] ?? category.replacingOccurrences(of: "_", with: " ")
    }

    /// The segment playback is inside, if it is one of the skippable ones. The
    /// small margin stops a seek landing just before a boundary from looping.
    public static func segmentToSkip(at time: Double, in segments: [SponsorSegment]) -> SponsorSegment? {
        segments.first { segment in
            autoSkip.contains(segment.category)
                && segment.end > segment.start
                && time >= segment.start
                && time < segment.end - 0.5
        }
    }

    /// Left/width fractions for each segment, clamped to the bar.
    public static func ranges(_ segments: [SponsorSegment], duration: Double) -> [SponsorRange] {
        guard duration > 0 else { return [] }
        return segments.compactMap { segment in
            let left = min(max(segment.start / duration, 0), 1)
            let right = min(max(segment.end / duration, 0), 1)
            guard right > left else { return nil }
            return SponsorRange(category: segment.category, start: left, width: right - left)
        }
    }
}
