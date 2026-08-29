import Foundation

/// What SponsorBlock does to one playback tick.
///
/// Every field is "nothing to do" when it is `nil`, so a player applies what it
/// is given and asks no questions.
public struct SponsorTick: Sendable, Hashable {
    /// Seek here: playback is inside a segment set to *skip*.
    public let skipTo: Double?
    /// Set the player's mute to this. It covers both ends of a `mute`
    /// segment — muting at the start, and giving the viewer their own setting
    /// back at the end.
    public let muted: Bool?
    /// The label of the segment just skipped, for the "Skipped the sponsor"
    /// note. Set on the tick that skips and never again.
    public let skippedLabel: String?

    public init(skipTo: Double? = nil, muted: Bool? = nil, skippedLabel: String? = nil) {
        self.skipTo = skipTo
        self.muted = muted
        self.skippedLabel = skippedLabel
    }

    public static let nothing = SponsorTick()
}

/// SponsorBlock applied to playback, one tick at a time.
///
/// Both players drive it — the phone through `PlayerEngine`, the TV through
/// `AVPlayer` — which is the point of it living here: what a category does is
/// ``SponsorRules``' decision, remembering a `mute` segment across ticks is
/// ``SponsorMuteTracker``'s, and the order the two happen in was written twice
/// until this existed. It decides and returns; performing the seek is the
/// player's, because nothing else in this package knows what a player is.
@MainActor
public final class SponsorRunner {
    private var mute = SponsorMuteTracker()

    public init() {}

    public func tick(
        at time: Double,
        segments: [SponsorSegment],
        prefs: Prefs,
        isMuted: Bool
    ) -> SponsorTick {
        let skip = SponsorRules.segmentToSkip(at: time, in: segments, prefs: prefs)
        return SponsorTick(
            skipTo: skip?.end,
            muted: mute.mute(at: time, in: segments, prefs: prefs, isMuted: isMuted),
            skippedLabel: skip.map { SponsorRules.label($0.category) }
        )
    }

    /// Forgets a `mute` segment in progress. A new video starts with the
    /// viewer's own mute setting, not one this segment imposed.
    public func reset() { mute = SponsorMuteTracker() }
}
