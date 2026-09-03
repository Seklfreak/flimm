import FlimmKit

/// What the keyboard shortcuts do, for the two that are a step along a list
/// rather than a single call: `[` / `]` between chapters and `,` / `.` between
/// speeds. See ``PlayerShortcut``.
///
/// An extension because ``WatchModel`` is at the size a class of its kind
/// should stop growing at, and these two are the easiest thing in it to name
/// on their own — neither is about playing a video, only about what a key
/// press means.
extension WatchModel {
    /// `[` and `]`. The maths is `FlimmKit`'s, shared with the web client, so
    /// "back to the start of this chapter" behaves identically in both.
    func jumpChapter(_ direction: Int) {
        let time = engine.currentTime
        let target = direction < 0
            ? ChapterMath.previousStart(before: time, in: chapters)
            : ChapterMath.nextStart(after: time, in: chapters)
        guard let target else { return }
        seek(to: target)
    }

    /// `,` and `.` — one step along the same speed list the menu offers.
    func stepSpeed(_ direction: Int) async {
        let speeds = PlaybackSpeeds.all
        let current = speeds.firstIndex(of: prefs.playbackSpeed) ?? speeds.firstIndex(of: 1.0) ?? 0
        let next = min(max(current + direction, 0), speeds.count - 1)
        guard next != current else { return }
        await setSpeed(speeds[next])
    }
}
