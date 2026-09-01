import UIKit

/// The auto-lock hold, taken while video is on screen.
///
/// `AVPlayerViewController` keeps the screen up on its own, which is why the TV
/// app never asks for this, but this player is a bare `AVPlayerLayer` under
/// controls of our own and has to say so itself:
/// `AVPlayer.preventsDisplaySleepDuringVideoPlayback` alone did not stop the
/// phone dimming and then locking mid-video.
///
/// `UIApplication.isIdleTimerDisabled` is one flag for the whole app, so the
/// holders are counted rather than the flag assigned. Starting a video while
/// another is open builds the new ``WatchModel`` before the old one tears down
/// (``PlayerCoordinator/play(_:context:startAt:)``), and the departing player
/// must not hand the screen back out from under the one that replaced it.
@MainActor
enum ScreenSleep {
    private static var holders: Set<ObjectIdentifier> = []

    /// Takes or drops `owner`'s hold. Idempotent, so it can be driven straight
    /// from a state change without tracking what was asked for last.
    static func hold(_ wanted: Bool, for owner: AnyObject) {
        let id = ObjectIdentifier(owner)
        if wanted { holders.insert(id) } else { holders.remove(id) }
        UIApplication.shared.isIdleTimerDisabled = !holders.isEmpty
    }
}
