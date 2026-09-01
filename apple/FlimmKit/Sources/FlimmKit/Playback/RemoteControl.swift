import Foundation
import Observation

/// The controller's half of remote control: what is playing elsewhere, and the
/// buttons that steer it.
///
/// One long poll is open for as long as this is running, and it answers the
/// moment anything changes on any of the account's screens — so a television
/// paused with its own remote shows as paused here within a round trip, and no
/// timer is polling a server that has nothing to say.
///
/// A press is echoed locally before the server confirms it. The alternative is a
/// play button that stays on "play" for the length of a round trip, which reads
/// as a control that did not work, and gets pressed twice.
@MainActor
@Observable
public final class RemoteControl {
    /// After a failed poll. Long enough not to hammer a server that is down,
    /// short enough that walking back into Wi-Fi reconnects before it is
    /// noticed.
    private static let retry = Duration.seconds(3)

    /// Every screen of this account's that is playing, most recently heard from
    /// first.
    public private(set) var sessions: [RemoteSession] = []
    /// When ``sessions`` arrived *here*. The clock is run forward from this and
    /// never from the server's timestamp; see ``RemoteClock``.
    public private(set) var receivedAt = Date()
    /// False while the poll is failing — the server is unreachable, or this
    /// device is offline. What is on screen is then the last thing known.
    public private(set) var isReachable = true

    /// The session being steered. The most recently active one unless a
    /// specific screen was chosen.
    public var current: RemoteSession? {
        if let attached, let match = sessions.first(where: { $0.id == attached }) { return match }
        return sessions.first
    }

    public var isPlayingSomewhere: Bool { current != nil }

    @ObservationIgnored private let client: APIClient
    @ObservationIgnored private var attached: String?
    @ObservationIgnored private var version: UInt64?
    @ObservationIgnored private var task: Task<Void, Never>?

    public init(client: APIClient) {
        self.client = client
    }

    /// Starts watching. Idempotent: calling it while it is already running does
    /// nothing, so a view can ask on every appearance.
    public func start() {
        guard task == nil else { return }
        task = Task { [weak self] in
            await self?.run()
        }
    }

    /// Stops watching, and drops the open request with it.
    ///
    /// Worth doing whenever the app is not on screen: this holds a connection
    /// open indefinitely, and a backgrounded phone has nobody to show a
    /// scrubber to.
    public func stop() {
        task?.cancel()
        task = nil
    }

    /// Steers a particular screen rather than whichever spoke last. Only
    /// matters when two are playing at once.
    public func attach(to id: String?) {
        attached = id
    }

    /// Where the session's playback is right now, run forward from the last
    /// thing it said.
    public func position(now: Date = Date()) -> Double {
        guard let current else { return 0 }
        return RemoteClock.position(of: current, receivedAt: receivedAt, now: now)
    }

    // MARK: - Commands

    /// Sends a command to the attached session, echoing it locally first.
    public func send(_ command: RemoteCommand) async {
        guard let session = current else { return }
        apply(command, to: session)
        do {
            try await client.sendRemoteCommand(session.id, command)
        } catch {
            // The echo was a guess and the guess was wrong. Rather than roll it
            // back into a flicker, let the next poll — which is already open and
            // will answer within the heartbeat — say what actually happened.
            isReachable = false
        }
    }

    public func togglePlayPause() async {
        guard let current else { return }
        await send(current.paused ? .play : .pause)
    }

    public func seek(to seconds: Double) async {
        await send(.seek(to: seconds))
    }

    public func skip(_ seconds: Double) async {
        await send(.skip(seconds))
    }

    public func goNext() async {
        guard current?.canNext == true else { return }
        await send(.next)
    }

    public func goPrevious() async {
        guard current?.canPrevious == true else { return }
        await send(.previous)
    }

    // MARK: - Internals

    private func run() async {
        // The first ask carries no version, so it answers at once with whatever
        // is playing. Every one after it is a long poll.
        var since: UInt64?
        while !Task.isCancelled {
            do {
                let page = try await client.remoteSessions(since: since)
                guard !Task.isCancelled else { return }
                sessions = page.sessions
                receivedAt = Date()
                version = page.version
                since = page.version
                isReachable = true
                // A screen that stopped playing is no longer a screen to steer.
                if let attached, !page.sessions.contains(where: { $0.id == attached }) {
                    self.attached = nil
                }
            } catch {
                guard !Task.isCancelled else { return }
                isReachable = false
                try? await Task.sleep(for: Self.retry)
            }
        }
    }

    /// The local echo. Only the fields a press plainly changes — everything
    /// else waits to be told.
    private func apply(_ command: RemoteCommand, to session: RemoteSession) {
        guard let action = command.action else { return }
        let position = RemoteClock.position(of: session, receivedAt: receivedAt)
        let echoed: RemoteSession?
        switch action {
        case .play:
            echoed = session.moved(position: position, paused: false, speed: session.speed)
        case .pause:
            echoed = session.moved(position: position, paused: true, speed: session.speed)
        case .seek:
            echoed = session.moved(position: command.position, paused: session.paused, speed: session.speed)
        case .skip:
            echoed = session.moved(position: max(0, position + command.delta), paused: session.paused, speed: session.speed)
        case .next, .previous:
            // Which video comes next is the player's answer, not one to guess
            // at: the title would be wrong for as long as the echo stood.
            echoed = nil
        }
        guard let echoed, let index = sessions.firstIndex(where: { $0.id == session.id }) else { return }
        sessions[index] = echoed
        receivedAt = Date()
    }
}
