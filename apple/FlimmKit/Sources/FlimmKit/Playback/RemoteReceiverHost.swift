import Foundation

/// The television's half of casting: says this screen can be asked to play
/// something, and opens what it is asked to.
///
/// It runs for as long as the app is on screen — not just while a video plays —
/// because the point is to be reachable from a phone *before* anything is
/// playing. Once the requested video plays, that player publishes a
/// ``RemoteSession`` through its own ``RemotePublisher`` and is steered through
/// that; this class has nothing more to do with it.
@MainActor
public final class RemoteReceiverHost {
    /// Opens a video. The receiver does what selecting it with the remote
    /// would, with the given start position and context.
    public typealias OpenHandler = @MainActor (RemoteCommand.OpenRequest) -> Void

    /// How often the offer is renewed. Well inside the server's 45 s expiry,
    /// so one failed request does not make the television vanish from a phone.
    private static let heartbeat = Duration.seconds(15)
    /// After a failed poll; see ``RemotePublisher``.
    private static let retry = Duration.seconds(2)

    /// This receiver's id. A new one for every ``start(onOpen:)``, so the
    /// retirement of a previous run — which may land late — can never take a
    /// fresh registration down with it.
    public private(set) var receiverId = UUID().uuidString

    private let client: APIClient
    private let device: String
    private let platform: String
    private var onOpen: OpenHandler?
    private var cursor: UInt64 = 0
    private var ticker: Task<Void, Never>?
    private var poller: Task<Void, Never>?

    public init(client: APIClient, device: String, platform: String) {
        self.client = client
        self.device = device
        self.platform = platform
    }

    public var isRunning: Bool { ticker != nil }

    /// Starts offering this screen. Idempotent, so a scene that becomes active
    /// twice does not register twice.
    public func start(onOpen: @escaping OpenHandler) {
        self.onOpen = onOpen
        guard ticker == nil else { return }
        receiverId = UUID().uuidString
        cursor = 0
        let receiver = RemoteReceiver(device: device, platform: platform)
        ticker = Task { [weak self, client, receiverId] in
            while !Task.isCancelled {
                // A failure is not worth reporting: the phone simply does not
                // offer this screen until the next beat gets through.
                try? await client.registerRemoteReceiver(receiverId, receiver)
                try? await Task.sleep(for: Self.heartbeat)
                guard self != nil else { return }
            }
        }
        poller = Task { [weak self, receiverId] in
            await self?.listen(receiverId)
        }
    }

    /// Withdraws the offer, so a phone stops listing this screen now rather
    /// than when it expires. The listening stops at once; the retirement is
    /// sent on its way and not waited for.
    public func stop() {
        guard ticker != nil else { return }
        ticker?.cancel()
        ticker = nil
        poller?.cancel()
        poller = nil
        Task { [client, receiverId] in try? await client.retireRemoteReceiver(receiverId) }
    }

    private func listen(_ receiverId: String) async {
        while !Task.isCancelled {
            do {
                let batch = try await client.receiverCommands(receiverId, after: cursor)
                // A poll that outlived its run must not touch the next run's
                // cursor.
                guard !Task.isCancelled else { return }
                // Adopt the cursor whether or not anything came with it; see
                // ``RemoteCommandBatch``.
                cursor = batch.cursor
                // Two casts in one batch: the later one is what was meant.
                if let request = batch.commands.compactMap(\.openRequest).last {
                    onOpen?(request)
                }
            } catch {
                // Includes the 404 before the first heartbeat has landed, and
                // after a server restart: the ticker re-registers, and the next
                // poll finds it.
                guard !Task.isCancelled else { return }
                try? await Task.sleep(for: Self.retry)
            }
        }
    }
}
