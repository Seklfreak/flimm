import Foundation

/// The player's half of remote control: says what it is doing, and does what it
/// is told.
///
/// A player hands over a closure that reads its current state, rather than
/// pushing updates in. That is deliberate. A paused `AVPlayer` stops calling its
/// periodic time observer altogether, so a publisher driven by the player's
/// ticks would go silent exactly when someone is most likely to be looking at
/// their phone to un-pause it — and the session would lapse under them. Owning
/// the tick means a paused player keeps saying so.
///
/// What is published, and when, is ``RemotePublishRule`` — not this class, and
/// certainly not the caller.
@MainActor
public final class RemotePublisher {
    /// Reads the player's state right now. `nil` means "nothing is playing",
    /// which retires the session.
    public typealias StateProvider = @MainActor () -> RemoteSession?
    /// Applies one command. Returning is not an acknowledgement — the state
    /// published afterwards is.
    public typealias CommandHandler = @MainActor (RemoteCommand) -> Void

    /// How often the rule is asked. Fast enough that a pause reaches the phone
    /// while the viewer is still looking at it, cheap enough that it costs
    /// nothing when the answer is no — which it is for all but one tick in ten.
    private static let tick = Duration.milliseconds(1000)
    /// After a failed poll. The session is republished by the ticker meanwhile,
    /// so this only paces the retry of a server that is down or has forgotten
    /// us.
    private static let retry = Duration.seconds(2)

    /// This playback session's id, chosen here and kept until ``stop()``.
    public nonisolated let sessionId: String

    private let client: APIClient
    private let device: String
    private let platform: String

    private var provider: StateProvider?
    private var onCommand: CommandHandler?
    private var published: RemoteSession?
    private var publishedAt: Date?
    private var isPublishing = false
    private var cursor: UInt64 = 0
    private var ticker: Task<Void, Never>?
    private var poller: Task<Void, Never>?

    public init(
        client: APIClient,
        device: String,
        platform: String,
        sessionId: String = UUID().uuidString
    ) {
        self.client = client
        self.device = device
        self.platform = platform
        self.sessionId = sessionId
    }

    /// Begins publishing and listening. Safe to call again; the previous run is
    /// stopped first.
    public func start(state: @escaping StateProvider, onCommand: @escaping CommandHandler) {
        cancelTasks()
        self.provider = state
        self.onCommand = onCommand
        published = nil
        publishedAt = nil
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: Self.tick)
            }
        }
        poller = Task { [weak self] in
            await self?.listen()
        }
    }

    /// Publishes now if anything a controller could not have derived has
    /// changed. Called on every tick, and worth calling directly the moment a
    /// player knows something moved.
    public func refresh() async {
        guard !isPublishing, let session = provider?() else { return }
        let next = stamped(session)
        let now = Date()
        guard RemotePublishRule.shouldPublish(previous: published, sent: publishedAt, next: next, now: now) else {
            return
        }
        isPublishing = true
        defer { isPublishing = false }
        do {
            try await client.publishRemoteSession(sessionId, next)
            published = next
            publishedAt = now
        } catch {
            // Nothing to do and nothing to tell the viewer: the phone simply
            // does not see this screen for now, and the next tick tries again.
            // Leaving `published` alone is what makes that next tick publish.
        }
    }

    /// Retires the session, so a controller learns the screen went dark now
    /// rather than when the session expires.
    public func stop() async {
        cancelTasks()
        provider = nil
        onCommand = nil
        published = nil
        publishedAt = nil
        try? await client.endRemoteSession(sessionId)
    }

    // MARK: - Internals

    private func cancelTasks() {
        ticker?.cancel()
        ticker = nil
        poller?.cancel()
        poller = nil
    }

    /// The long-poll loop. One request is open for as long as nobody presses
    /// anything, which is what makes a press arrive in the time of a round trip
    /// rather than at the top of the next poll.
    private func listen() async {
        while !Task.isCancelled {
            do {
                let batch = try await client.remoteCommands(sessionId, after: cursor)
                // Adopt the cursor whether or not anything came with it: a
                // backlog that overflowed holds commands this player will never
                // see, and asking for them again for ever is the alternative.
                cursor = batch.cursor
                guard !Task.isCancelled else { return }
                for command in batch.commands {
                    // A kind this player does not know is a newer controller
                    // talking, and is skipped rather than guessed at.
                    guard command.action != nil else { continue }
                    onCommand?(command)
                }
                if !batch.commands.isEmpty {
                    // Let the controller see the result of what it pressed
                    // without waiting for the next tick.
                    await refresh()
                }
            } catch {
                // Includes the 404 a server that restarted answers with: the
                // ticker republishes the session, and the next poll finds it.
                guard !Task.isCancelled else { return }
                try? await Task.sleep(for: Self.retry)
            }
        }
    }

    /// Fills in what this player knows about itself and the server does not
    /// take from the body anyway.
    private func stamped(_ session: RemoteSession) -> RemoteSession {
        RemoteSession(
            id: sessionId,
            device: device,
            platform: platform,
            videoId: session.videoId,
            title: session.title,
            channelName: session.channelName,
            thumbUrl: session.thumbUrl,
            position: session.position,
            duration: session.duration,
            paused: session.paused,
            speed: session.speed,
            audioOnly: session.audioOnly,
            canNext: session.canNext,
            canPrevious: session.canPrevious
        )
    }
}
