import Foundation

/// How long "Resumed from 12:31 · Start over" stays on screen.
///
/// It is an offer, not a status: worth making while the viewer is still
/// working out where they are, and in the way after that. The window is
/// measured in *playback* rather than wall clock, so a paused player keeps
/// the offer up — someone who paused to decide has not had their minute.
///
/// The web client applies the same rule (`RESUME_NOTICE_SECONDS` in
/// `frontend/src/player/Player.tsx`).
public enum ResumeNotice {
    /// Seconds of playback past the resume point.
    public static let window: TimeInterval = 60

    /// Whether the chip should still be shown for a resume at `resumedFrom`
    /// with playback now at `currentTime`.
    ///
    /// Callers clear their own `resumedFrom` when this turns false, so seeking
    /// back past the resume point does not bring the chip back — it was
    /// answered once.
    public static func isVisible(resumedFrom: Double, currentTime: Double) -> Bool {
        currentTime - resumedFrom < window
    }
}
