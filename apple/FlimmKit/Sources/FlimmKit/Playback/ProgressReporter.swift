import Foundation

/// Sends the playback heartbeat.
///
/// `POST /videos/{id}/progress` every ~10s while playing, and once more on
/// pause, seek, background and termination. The reporter decides *when* to
/// post; the server decides what the position means — what counts as watched
/// and what is too brief to record at all. Neither rule is duplicated here.
public actor ProgressReporter {
    /// Reads the player's current position, in seconds.
    public typealias PositionProvider = @Sendable () async -> Double

    /// Called after every accepted heartbeat, so a client can flip its UI to
    /// "watched" at the moment the server decides so.
    public typealias ResultHandler = @Sendable (ProgressResult) async -> Void

    private let client: APIClient
    private let interval: Duration
    private var handler: ResultHandler?

    private var videoId: String?
    private var playlistId: String?
    private var position: PositionProvider?
    private var ticker: Task<Void, Never>?
    private var lastReported: Double?

    public init(client: APIClient, interval: Duration = .seconds(10)) {
        self.client = client
        self.interval = interval
    }

    public func onResult(_ handler: ResultHandler?) {
        self.handler = handler
    }

    /// Begin reporting for a video. Any previous run is flushed and stopped
    /// first, so stepping to the next video never loses the last position.
    public func start(videoId: String, context: PlaybackContext = .none, position: @escaping PositionProvider) async {
        await stop()
        self.videoId = videoId
        self.playlistId = context.playlistId
        self.position = position
        self.lastReported = nil
        resume()
    }

    /// Start the periodic heartbeat (playback began or resumed).
    public func resume() {
        guard videoId != nil, ticker == nil else { return }
        let interval = self.interval
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: interval)
                if Task.isCancelled { return }
                await self?.flush()
            }
        }
    }

    /// Stop the periodic heartbeat and post once. Call on pause, seek,
    /// backgrounding and termination — the four moments a position would
    /// otherwise be lost.
    public func pause() async {
        ticker?.cancel()
        ticker = nil
        await flush()
    }

    /// Post the current position now, whether or not the ticker is running.
    public func flush() async {
        guard let videoId, let position else { return }
        let seconds = await position()
        // Sub-second repeats of the same instant say nothing; a real pause
        // still gets through because the position has moved by then.
        if let lastReported, abs(lastReported - seconds) < 1 { return }
        lastReported = seconds
        do {
            let result = try await client.reportProgress(videoId, position: seconds, playlistId: playlistId)
            await handler?(result)
        } catch {
            // A dropped heartbeat is not worth surfacing: the next one carries
            // a later position, and the server keeps the newest it saw.
            lastReported = nil
        }
    }

    /// Flush and forget the video. Safe to call twice.
    public func stop() async {
        ticker?.cancel()
        ticker = nil
        await flush()
        videoId = nil
        playlistId = nil
        position = nil
        lastReported = nil
    }
}
