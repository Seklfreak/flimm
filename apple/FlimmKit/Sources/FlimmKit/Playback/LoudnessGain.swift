import Foundation

// Loudness normalisation, on the client's side of it.
//
// The server measures a video once (EBU R128) and says how many decibels to
// move it by; a client asks, waits for the answer, and sets the player's
// volume. Nothing here decides anything about level — that is the server's
// job, so the phone and the TV can never disagree about how loud a video is.

/// `GET /videos/{id}/loudness` — how loud a video was measured to be, and what
/// to do about it.
public struct LoudnessInfo: Codable, Sendable, Hashable {
    /// Where the measurement pass stands. `pending`/`running` mean "ask
    /// again"; only `done` carries numbers.
    public let state: HLSState
    /// The decibels to apply. Never positive: no client can amplify uniformly
    /// (`AVPlayer`'s volume stops at 1, and an audio mix does not apply to an
    /// HLS stream), so normalisation only ever turns a video down.
    public let gainDB: Double
    /// The programme loudness the gain aims at, so nothing hardcodes it.
    public let targetLUFS: Double
    public let measuredLUFS: Double
    public let peakDBTP: Double
    public let rangeLU: Double

    public init(
        state: HLSState = .pending,
        gainDB: Double = 0,
        targetLUFS: Double = 0,
        measuredLUFS: Double = 0,
        peakDBTP: Double = 0,
        rangeLU: Double = 0
    ) {
        self.state = state
        self.gainDB = gainDB
        self.targetLUFS = targetLUFS
        self.measuredLUFS = measuredLUFS
        self.peakDBTP = peakDBTP
        self.rangeLU = rangeLU
    }

    /// `.convertFromSnakeCase` turns `gain_db` into `gainDb` and `peak_dbtp`
    /// into `peakDbtp` — neither matches the acronym spellings used here, and
    /// a silent mismatch would decode as a gain of 0 on every server, which is
    /// indistinguishable from "not measured yet".
    private enum CodingKeys: String, CodingKey {
        case state
        case gainDB = "gainDb"
        case targetLUFS = "targetLufs"
        case measuredLUFS = "measuredLufs"
        case peakDBTP = "peakDbtp"
        case rangeLU = "rangeLu"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decode(.state, or: HLSState.unknown)
        gainDB = try c.decode(.gainDB, or: 0)
        targetLUFS = try c.decode(.targetLUFS, or: 0)
        measuredLUFS = try c.decode(.measuredLUFS, or: 0)
        peakDBTP = try c.decode(.peakDBTP, or: 0)
        rangeLU = try c.decode(.rangeLU, or: 0)
    }
}

public enum LoudnessGain {
    /// A gain in decibels as the linear scale `AVPlayer.volume` takes.
    ///
    /// A positive gain is refused rather than clamped upward: the player would
    /// silently ignore anything above 1, so pretending to boost would make the
    /// TV and the web disagree about a video's level.
    public static func volume(forGainDB gainDB: Double) -> Float {
        guard gainDB.isFinite, gainDB < 0 else { return 1 }
        return Float(max(pow(10, gainDB / 20), 0))
    }

    /// Loads a video's measurement, waiting out the pass.
    ///
    /// Asking is what starts it, so a first answer of `running` is the normal
    /// case rather than a failure; this asks again a few times and then gives
    /// up, leaving the video at the level it was archived at. The next time it
    /// is played, the measurement is on disk.
    public static func load(videoID: String, client: APIClient) async -> LoudnessInfo? {
        for (attempt, gap) in retryGaps.enumerated() {
            if attempt > 0 {
                guard (try? await Task.sleep(nanoseconds: UInt64(gap * 1_000_000_000))) != nil else { return nil }
            }
            guard let info = try? await client.loudness(videoID) else { return nil }
            if info.state == .done { return info }
            if info.state == .failed || info.state == .unknown { return nil }
            if Task.isCancelled { return nil }
        }
        return nil
    }

    /// The first entry is the first try, so only the later ones are waits.
    /// The pass decodes the audio of the whole file, which is quick for a
    /// ten-minute video and not for a three-hour one.
    private static let retryGaps: [Double] = [0, 3, 8, 20]
}
