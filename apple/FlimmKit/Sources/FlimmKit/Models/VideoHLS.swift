import Foundation

// The compatible H.264/AAC rendition ladder, as the video detail and
// `POST /videos/{id}/hls` report it. Split out of ``Video`` because it is a
// self-contained corner of the contract: a client that plays the archived file
// directly never touches any of it.

/// Where the compatible H.264/AAC rendition (`hls_url`) stands, from
/// `hls_state` on the video detail.
///
/// `unknown` is not a server state: it is what an unrecognised one decodes to,
/// so a value added to the contract later cannot fail the whole video detail.
public enum HLSState: String, Codable, Sendable, CaseIterable {
    /// Nobody has asked for it; the first request starts a transcode.
    case pending
    /// Being transcoded, or queued behind another transcode.
    case running
    /// On disk; playback starts immediately.
    case done
    /// The last attempt failed; the next request tries again.
    case failed
    case unknown

    public init(from decoder: any Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = HLSState(rawValue: raw) ?? .unknown
    }

    /// True while the rendition is being made — the states a player shows
    /// "preparing a compatible version…" for.
    public var isPreparing: Bool { self == .pending || self == .running }
}

/// The codec one rendition of the ladder is encoded in: `h264` at 1080p and
/// below, `hevc` at 1440 and 2160 (an H.264 encode of 4K is enormous, and 4K
/// H.264 is past what Apple's decoders are specified for).
///
/// Every device these apps run on decodes HEVC in hardware — the iPhone 7 and
/// the first Apple TV 4K onwards — but that is checked at runtime rather than
/// assumed; see ``DeviceCapabilities``. `unknown` is not a server value: it is
/// what an unrecognised one decodes to, so a codec added to the contract later
/// cannot fail the whole video detail.
public enum HLSCodec: String, Codable, Sendable, CaseIterable {
    case h264
    case hevc
    case unknown

    public init(from decoder: any Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = HLSCodec(rawValue: raw) ?? .unknown
    }
}

/// One rung of the quality ladder (`hls_variants`), each a rendition in its own
/// right: its own URL, its own cache entry, its own `state`, derived only when
/// something asks for it. The states within one video therefore differ.
public struct HLSVariant: Codable, Sendable, Hashable, Identifiable {
    /// The rendition's height in pixels; the width follows the source's
    /// aspect ratio. One of 2160, 1440, 1080, 720, 480.
    public let height: Int
    /// The playlist to load, `/media/hls/{id}/{height}/index.m3u8`.
    public let url: String
    /// Where *this* height stands, exactly as ``HLSState``.
    public let state: HLSState
    public let codec: HLSCodec
    /// How much of this rendition has been encoded, 0…1. Only meaningful
    /// while ``state`` is `running`; 0 on a backend that predates the field,
    /// which is the same value it has before a transcode starts.
    public let progress: Double

    public var id: Int { height }

    public init(height: Int, url: String, state: HLSState = .pending, codec: HLSCodec = .h264, progress: Double = 0) {
        self.height = height
        self.url = url
        self.state = state
        self.codec = codec
        self.progress = progress
    }

    /// Explicit, like every other URL-carrying key in this file:
    /// `.convertFromSnakeCase` is close enough to right here that a silent
    /// mismatch would be easy to miss. The backend spells the fraction
    /// `hls_progress`; ``HLSProgressKeys`` takes a bare `progress` too, so
    /// which of the two the contract settles on cannot silently decode as 0.
    private enum CodingKeys: String, CodingKey {
        case height, url, state, codec
        case progress = "hlsProgress"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        height = try c.decode(.height, or: 0)
        url = try c.decode(.url, or: "")
        state = try c.decode(.state, or: HLSState.unknown)
        codec = try c.decode(.codec, or: HLSCodec.unknown)
        progress = try HLSProgressKeys.decode(from: decoder, hlsPrefixed: c, key: .progress)
    }
}

/// Both spellings of the transcode's progress fraction.
///
/// `POST /videos/{id}/hls` and each `hls_variants` entry report it as
/// `hls_progress`, which `.convertFromSnakeCase` turns into `hlsProgress`; a
/// bare `progress` is read as well, so a rename on either side degrades to the
/// other spelling rather than to a silent 0. Missing everywhere means "the
/// server has not said", which is 0.
enum HLSProgressKeys: String, CodingKey {
    case progress

    static func decode<K: CodingKey>(
        from decoder: any Decoder,
        hlsPrefixed container: KeyedDecodingContainer<K>,
        key: K
    ) throws -> Double {
        if let value = try? container.decode(Double.self, forKey: key) { return value }
        let plain = try decoder.container(keyedBy: HLSProgressKeys.self)
        return try plain.decode(.progress, or: 0)
    }
}

/// `POST /videos/{id}/hls` response — where the compatible rendition stands
/// after the call. The call is idempotent: a rendition that is already running
/// is steered rather than started again.
public struct HLSStatus: Codable, Sendable, Hashable {
    public let state: HLSState
    /// How much of the rendition has been encoded, 0…1 — what a player shows
    /// as "Preparing… 37%" while it waits. Only meaningful while ``state`` is
    /// `running`; 0 on a backend that predates the field.
    public let progress: Double

    public init(state: HLSState, progress: Double = 0) {
        self.state = state
        self.progress = progress
    }

    /// `hls_progress` is the backend's spelling; see ``HLSProgressKeys``.
    private enum CodingKeys: String, CodingKey {
        case state
        case progress = "hlsProgress"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decode(.state, or: HLSState.unknown)
        progress = try HLSProgressKeys.decode(from: decoder, hlsPrefixed: c, key: .progress)
    }
}
