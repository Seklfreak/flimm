import Foundation

/// The work both players do on every playback tick that is not about drawing
/// anything: measure the video's loudness once, notice the picture stopping,
/// and apply SponsorBlock.
///
/// Each of those was written twice, in two models that were already at the size
/// a file is allowed to be — and each is a rule rather than a platform detail,
/// so the phone and the TV must not be able to drift apart on any of them. This
/// owns the three collaborators and is called once per tick from each.
///
/// It performs nothing on the player itself: the SponsorBlock decision comes
/// back for the caller to apply, and the loudness gain arrives through a
/// closure, because only the caller knows what a player is.
@MainActor
public final class PlaybackServices {
    /// What a tick knows. A struct rather than nine parameters, and because
    /// every field here is something a player can answer without thinking.
    public struct Tick {
        public let videoID: String
        public let time: Double
        /// The player is trying to play and has nothing to play — a stall, as
        /// opposed to a pause.
        public let isStalled: Bool
        /// The compatible rendition's height, or 0 when the archived file is
        /// playing directly. It is what lets the server attribute a stall.
        public let height: Int
        public let segments: [SponsorSegment]
        public let prefs: Prefs
        public let isMuted: Bool

        public init(
            videoID: String,
            time: Double,
            isStalled: Bool,
            height: Int,
            segments: [SponsorSegment],
            prefs: Prefs,
            isMuted: Bool
        ) {
            self.videoID = videoID
            self.time = time
            self.isStalled = isStalled
            self.height = height
            self.segments = segments
            self.prefs = prefs
            self.isMuted = isMuted
        }
    }

    private let client: APIClient
    private let sponsors = SponsorRunner()
    private let loudness = LoudnessNormalizer()
    private let stalls: StallReporter

    public init(client: APIClient, platform: String) {
        self.client = client
        self.stalls = StallReporter(client: client, platform: platform)
    }

    /// The measurement behind the gain now applied, for the playback stats
    /// panel. Nothing about playback reads it.
    public var loudnessInfo: LoudnessInfo? { loudness.latest }

    /// One tick. Returns what SponsorBlock wants done; everything else is
    /// handled here or handed back through `setGain`.
    @discardableResult
    public func tick(_ tick: Tick, setGain: @escaping @MainActor (Double) -> Void) -> SponsorTick {
        stalls.update(
            isStalled: tick.isStalled,
            videoID: tick.videoID,
            position: tick.time,
            height: tick.height
        )
        // Not before playback: the measurement decodes the whole file, and
        // starting it during a transcode makes the viewer wait for their own
        // video. Idempotent per video, so a tick costs nothing.
        loudness.apply(
            videoID: tick.videoID,
            enabled: tick.prefs.normalizeLoudness,
            client: client,
            setGain: setGain
        )
        return sponsors.tick(
            at: tick.time,
            segments: tick.segments,
            prefs: tick.prefs,
            isMuted: tick.isMuted
        )
    }

    /// A new video: forget a `mute` segment in progress and a stall that was
    /// never going to end, and let the next tick measure the new video.
    public func startingVideo() {
        sponsors.reset()
        stalls.reset()
    }

    /// Playback is over.
    public func stop() {
        loudness.cancel()
        stalls.reset()
    }
}
