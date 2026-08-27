import FlimmKit
import Foundation

/// Keeps the server's transcode pointed at the part of the video the viewer is
/// actually in, and reports how far it has got.
///
/// `POST /videos/{id}/hls?height=&from=` both starts a compatible rendition and
/// steers one that is already running: the encoder produces `from` onwards
/// first, which is what makes resuming an hour into a video immediate rather
/// than a wait for the forty minutes nobody is going to watch. The playlist is
/// a complete VOD one from its first request, so every seek lands whatever the
/// encoder has reached; a segment that does not exist yet simply blocks on the
/// server while it is made, and that stall is the only thing steering shortens.
///
/// ``HLSStatus/progress`` is the fraction of the whole rendition that exists,
/// which is what the "Preparing…" overlay counts up — not a distance from
/// ``from``, and not something to infer a produced region from.
///
/// One of these belongs to a watch session. iOS and tvOS share it because the
/// rule is the contract's, not the platform's.
@MainActor
final class RenditionSteering {
    /// Fired whenever the server has said something new about the job — a
    /// steer that was actually sent, or a poll while the viewer waits.
    var onStatus: (@MainActor (HLSStatus) -> Void)?

    private let client: APIClient
    private var videoId = ""
    private var height: Int?
    /// Where the encoder was last pointed.
    private var from: Double = 0
    private var state: HLSState?
    private var steerTask: Task<Void, Never>?
    private var pollTask: Task<Void, Never>?

    /// A scrub is dozens of seeks; only where it settles is worth telling the
    /// encoder about.
    private static let debounce: Duration = .seconds(1)
    /// How far from where the encoder was last pointed a seek has to land
    /// before it is worth saying anything at all.
    private static let tolerance: Double = 30
    private static let pollInterval: Duration = .seconds(5)

    init(client: APIClient) {
        self.client = client
    }

    /// Starts the rendition for the height about to play, from where playback
    /// will start, **without waiting** for it. The status comes back rather
    /// than through ``onStatus``, because the caller is already awaiting it.
    func start(videoId: String, height: Int?, from position: Double) async -> HLSStatus? {
        cancel()
        self.videoId = videoId
        self.height = height
        from = position
        state = nil
        let status = try? await client.startHLS(videoId, height: height, from: position)
        state = status?.state
        return status
    }

    /// Adopts what the video detail said, for when the call itself failed —
    /// a rendition already `done` is one nothing needs to steer or poll.
    func adopt(state fallback: HLSState?) {
        if state == nil { state = fallback }
    }

    /// Re-points the encoder at a seek that went somewhere else.
    ///
    /// The client deliberately does not model what has been produced: the
    /// server knows which segments exist, whether the encoder is already
    /// heading towards the one being asked for, and how often it is willing to
    /// be re-aimed — a `from` it does not need costs it nothing. So this only
    /// skips the obviously pointless (a finished rendition, a target where the
    /// encoder is already pointed, a video nobody is encoding) and debounces,
    /// because dragging a scrubber is dozens of seeks and only where it
    /// settles counts.
    func steer(to target: Double) {
        guard !videoId.isEmpty, isWorthSteering(target) else { return }
        steerTask?.cancel()
        steerTask = Task { [weak self] in
            try? await Task.sleep(for: Self.debounce)
            guard !Task.isCancelled, let self, self.isWorthSteering(target) else { return }
            self.from = target
            guard let status = try? await self.client.startHLS(self.videoId, height: self.height, from: target) else { return }
            self.apply(status)
        }
    }

    /// While the viewer is waiting — nothing on screen yet, or a stall on a
    /// segment still being encoded — asks how far the job has got, so the
    /// overlay can say "Preparing… 37%" rather than spin silently. It goes
    /// quiet while the rendition plays and ends once the server says the job
    /// is finished.
    func poll(while isWaiting: @escaping @MainActor () -> Bool) {
        guard !videoId.isEmpty else { return }
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: Self.pollInterval)
                guard !Task.isCancelled, let self, self.state?.isPreparing ?? true else { return }
                guard isWaiting() else { continue }
                guard let status = try? await self.client.startHLS(self.videoId, height: self.height, from: self.from) else { continue }
                self.apply(status)
            }
        }
    }

    /// Forgets the job as well as stopping the work, so a seek on the *next*
    /// video — the archived file, say — cannot re-point the last one.
    func cancel() {
        steerTask?.cancel()
        steerTask = nil
        pollTask?.cancel()
        pollTask = nil
        videoId = ""
        height = nil
        state = nil
        from = 0
    }

    private func apply(_ status: HLSStatus) {
        state = status.state
        onStatus?(status)
    }

    private func isWorthSteering(_ target: Double) -> Bool {
        guard state != .done else { return false }
        return abs(target - from) > Self.tolerance
    }
}
