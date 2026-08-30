import Foundation

/// Reports a stall — the picture stopping mid-playback — to the server.
///
/// A client is the only side that knows it happened: nothing fails, no request
/// errors, the viewer just watches a spinner. The server is the only side that
/// knows *why* it might have, because it knows where the encoder had got to and
/// whether the segment being waited for existed. So the client says what it was
/// playing and for how long it stopped, and the server attributes it.
///
/// Both Apple players own one. The rule about what counts as a stall lives here
/// rather than in either of them.
@MainActor
public final class StallReporter {
    /// Below this a "stall" is the ordinary gap between two segments, and
    /// reporting it would bury the ones a person noticed. The server applies
    /// the same floor; this one saves the request.
    private static let minimum: TimeInterval = 0.4

    private let client: APIClient
    private let platform: String
    private var began: Date?

    public init(client: APIClient, platform: String) {
        self.client = client
        self.platform = platform
    }

    /// Called on every tick with the player's own idea of whether it is
    /// waiting. The transition is what matters, so it is safe to call
    /// constantly with the same value.
    public func update(isStalled: Bool, videoID: String, position: Double, height: Int?) {
        switch (isStalled, began) {
        case (true, nil):
            began = Date()
        case (false, .some(let start)):
            began = nil
            let seconds = Date().timeIntervalSince(start)
            guard seconds >= Self.minimum, !videoID.isEmpty else { return }
            report(videoID: videoID, position: position, seconds: seconds, height: height)
        default:
            break
        }
    }

    /// Playback ended or the video changed: a stall in progress is abandoned
    /// rather than reported, because its length is unknown — the viewer may
    /// simply have left.
    public func reset() { began = nil }

    private func report(videoID: String, position: Double, seconds: Double, height: Int?) {
        let platform = platform
        let client = client
        Task {
            // Best effort by design: a report that fails is not worth telling
            // a viewer about, and never worth retrying into a server that is
            // already having a bad time.
            try? await client.reportStall(
                videoID,
                position: position,
                seconds: seconds,
                height: height ?? 0,
                client: platform
            )
        }
    }
}
