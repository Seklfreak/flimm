import Foundation

/// What a player does when the video reaches its end.
///
/// Autoplay advances only when there is something to advance to; every other
/// ending stays on the video, where an end card says so. Without that card a
/// finished video is a still frame, which is exactly what a paused one looks
/// like — the viewer is left to work out which they are looking at.
///
/// The phone and the TV ask this so they cannot drift on when the card
/// appears, and the web client applies the same rule
/// (`frontend/src/player/playbackEnd.ts`).
public enum PlaybackEnd: Equatable, Sendable {
    /// Autoplay takes over: the next video replaces this one.
    case advance
    /// Playback is over and stays here.
    case finished

    public static func decide(autoplay: Bool, hasNext: Bool) -> PlaybackEnd {
        autoplay && hasNext ? .advance : .finished
    }
}
