import Foundation

/// Plays each video at the level the server measured for it.
///
/// Both players own one of these: it asks for the measurement, waits it out
/// (see ``LoudnessGain/load(videoID:client:)``) and hands the gain back to
/// whoever knows how to set a volume. Everything about *what* the gain should
/// be is the server's; everything about *when* to apply it is here, so the
/// phone and the TV cannot drift apart on either.
@MainActor
public final class LoudnessNormalizer {
    private var task: Task<Void, Never>?
    /// What the last request was for, so a caller may ask on every playback
    /// tick — which is how both players wait for playback to actually begin
    /// before spending anything on this.
    private var applied: Key?
    /// The measurement behind the gain currently applied, kept for the
    /// playback stats panel and read by nothing else. A reading, not a
    /// decision: the gain has already been handed to the player by the time
    /// this is set, and setting it changes nothing about what you hear.
    public private(set) var latest: LoudnessInfo?

    private struct Key: Equatable {
        let videoID: String
        let enabled: Bool
    }

    public init() {}

    /// Starts normalising a video.
    ///
    /// **Call this from playback, not from loading it.** The measurement is a
    /// decode of the whole file server-side, and asking for it while a
    /// compatible rendition is still being transcoded puts a second ffmpeg
    /// between the viewer and their own video. Both players call it on a
    /// playback tick, which is why it is idempotent per video and per setting.
    ///
    /// The gain is reset to 0 first, and always: a new video plays at its
    /// archived level until its own measurement lands, and a viewer who turns
    /// the preference off hears that immediately rather than on the next
    /// video.
    public func apply(
        videoID: String,
        enabled: Bool,
        client: APIClient,
        setGain: @escaping @MainActor (Double) -> Void
    ) {
        let key = Key(videoID: videoID, enabled: enabled)
        guard applied != key else { return }
        applied = key
        task?.cancel()
        task = nil
        latest = nil
        setGain(0)
        guard enabled, !videoID.isEmpty else { return }
        task = Task {
            guard let info = await LoudnessGain.load(videoID: videoID, client: client),
                  !Task.isCancelled else {
                return
            }
            latest = info
            setGain(info.gainDB)
        }
    }

    public func cancel() {
        task?.cancel()
        task = nil
        applied = nil
        latest = nil
    }
}
