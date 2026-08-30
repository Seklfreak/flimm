import Foundation

/// How far a subtitle cue sits above the bottom of the picture.
///
/// Two numbers, because there are two situations and they answer to different
/// things.
///
/// **Idle** — nothing drawn over the video — is a *proportion* of the picture,
/// not a fixed inset. A flat 16pt reads as a comfortable margin on a phone and
/// as "stuck to the bottom edge" on an iPad, which is exactly what it looked
/// like: the same number against a picture three times the height. Broadcast
/// and YouTube both sit captions roughly a tenth of the way up, and that is
/// what this returns, with a floor so a very small player still clears its own
/// edge.
///
/// **Over chrome** is the opposite: it has nothing to do with the picture and
/// everything to do with the thing in the way, so it takes that thing's
/// measured height. A guessed constant is how captions end up sitting on the
/// scrubber the moment a viewer pauses — the controls are taller on an iPad
/// than on a phone, and the guess can only be right for one of them.
public enum SubtitleLift {
    /// The share of the picture's height a cue sits above the bottom edge.
    public static let idleFraction = 0.10
    /// Below this the proportion stops being a margin at all.
    public static let idleMinimum = 20.0
    /// Between the top of the chrome and the bottom of the cue.
    public static let chromeGap = 12.0

    /// The lift with nothing drawn over the video.
    public static func idle(pictureHeight: Double) -> Double {
        guard pictureHeight > 0 else { return idleMinimum }
        return max(idleMinimum, pictureHeight * idleFraction)
    }

    /// The lift with a control bar `barHeight` tall along the bottom edge.
    ///
    /// Never less than the idle lift: chrome that measures as very short (or
    /// has not been measured yet) must not drop a cue lower than it sits with
    /// no chrome at all.
    public static func overChrome(barHeight: Double, pictureHeight: Double) -> Double {
        max(idle(pictureHeight: pictureHeight), barHeight + chromeGap)
    }
}
