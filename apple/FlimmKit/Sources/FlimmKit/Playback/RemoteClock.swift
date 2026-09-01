import Foundation

/// Where a remote player's playback actually is, between heartbeats.
///
/// A television publishes its position every few seconds. A controller drawing
/// that number directly has a scrubber that jumps once a heartbeat and sits
/// still in between, which reads as a broken app rather than a slow one — so it
/// runs the clock forward itself, exactly as the television's own clock is
/// running.
///
/// The elapsed time is measured from **when the controller received the
/// session**, never from `updatedAt`. A phone's clock and a server's clock are
/// not the same clock, and the difference between them is unbounded — one
/// device with the wrong date would put the scrubber minutes out. The latency
/// of the response that delivered the session is bounded, and small.
public enum RemoteClock {
    /// The position to draw now.
    ///
    /// Paused playback stays where it is. Playing advances at the session's own
    /// speed, and stops at the duration — a controller must not run past the
    /// end of a video while waiting to hear that the next one started.
    public static func position(of session: RemoteSession, receivedAt: Date, now: Date = Date()) -> Double {
        guard !session.paused else { return clamp(session.position, to: session.duration) }
        let elapsed = max(0, now.timeIntervalSince(receivedAt))
        let speed = session.speed > 0 ? session.speed : 1
        return clamp(session.position + elapsed * speed, to: session.duration)
    }

    /// 0…1 for a progress bar. A session with no duration yet has no progress
    /// to report rather than a full bar.
    public static func progress(of session: RemoteSession, receivedAt: Date, now: Date = Date()) -> Double {
        guard session.duration > 0 else { return 0 }
        return position(of: session, receivedAt: receivedAt, now: now) / session.duration
    }

    private static func clamp(_ value: Double, to duration: Double) -> Double {
        let floored = max(0, value)
        guard duration > 0 else { return floored }
        return min(floored, duration)
    }
}

/// When a player should publish, given that its clock never stops moving.
///
/// A player ticks several times a second and cannot post that often. It also
/// cannot post only on a timer: a pause the viewer made with the Siri Remote has
/// to reach the phone in the time it takes to look at it, not on the next
/// heartbeat. So the rule is "publish what a controller could not have worked
/// out for itself" — anything that is not the clock ticking on as expected.
///
/// It lives here beside ``RemoteClock`` on purpose: this decides what a
/// controller is *allowed* to project, and that projects it. Loosening one
/// without the other is what makes a scrubber drift.
public enum RemotePublishRule {
    /// How long a session may go unpublished while nothing changes. Under a
    /// third of the server's 45s expiry, so two lost heartbeats do not make a
    /// television that is plainly still playing vanish from the phone.
    public static let heartbeat: TimeInterval = 10
    /// How far the position may differ from what a controller would have
    /// projected before it counts as a jump rather than drift. Comfortably
    /// above the ordinary error from a round trip, and far below the smallest
    /// seek any client offers (±10s).
    public static let driftTolerance: Double = 2

    public static func shouldPublish(
        previous: RemoteSession?,
        sent: Date?,
        next: RemoteSession,
        now: Date = Date()
    ) -> Bool {
        guard let previous, let sent else { return true }
        if changed(from: previous, to: next) { return true }
        if now.timeIntervalSince(sent) >= heartbeat { return true }
        // A seek: the clock is somewhere the controller's projection would not
        // have put it. Publishing this is what makes a jump on the television
        // show up on the phone at once rather than at the next heartbeat.
        let projected = RemoteClock.position(of: previous, receivedAt: sent, now: now)
        return abs(next.position - projected) > driftTolerance
    }

    /// Everything a controller cannot derive for itself: what is playing,
    /// whether it is paused, how fast, and whether stepping is possible. A
    /// change to any of them is the whole reason the controller is looking.
    private static func changed(from previous: RemoteSession, to next: RemoteSession) -> Bool {
        if previous.videoId != next.videoId { return true }
        if previous.title != next.title { return true }
        if previous.paused != next.paused { return true }
        if previous.speed != next.speed { return true }
        if previous.duration != next.duration { return true }
        if previous.audioOnly != next.audioOnly { return true }
        if previous.canNext != next.canNext { return true }
        if previous.canPrevious != next.canPrevious { return true }
        return false
    }
}
