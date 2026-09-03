import Foundation

// Playback stats: what a player is actually doing, said out loud.
//
// Everything Flimm derives is invisible by design — a transcode a viewer never
// asked for, a sheet of stills queued behind it, a measurement that quietly
// turns the volume down — and every one of them fails in a way that looks
// exactly like nothing happening. This is where they say so. The web client
// has had the panel since it had a player (`PlaybackStats.tsx`); this is the
// same panel's readings and the same vocabulary for Apple.
//
// Two rules carried over from there, both load-bearing:
//
// It **reads, and never decides**. Every value is handed in by the player that
// runs on it — the gate's own `reason`, the cache's own job states, the item's
// own counters. A panel that worked anything out for itself could disagree
// with the picture, which would make it worse than no panel.
//
// It is a **value, and `Codable`**, which the web's equivalent is not. On the
// television the panel is deliberately not on the television: the Apple TV
// publishes its readings in the remote session and the phone renders them.
// Sixteen numbers in small type at two metres is not a panel anybody reads,
// and the companion already exists for exactly that reason.

/// Why the codec gate landed where it did.
///
/// The same words as the web client's `PlaybackReason`, because they are the
/// contract's vocabulary rather than one client's prose — the two panels have
/// to answer "why is this transcoding?" the same way.
public enum PlaybackReason: String, Codable, Sendable, Hashable, CaseIterable {
    case audioOnly = "audio-only"
    case codecsUnknown = "codecs-unknown"
    case archiveDecodes = "archive-decodes"
    case archiveIsEnough = "archive-is-enough"
    case qualityPicked = "quality-picked"
    case noDecoder = "no-decoder"
    case noRung = "no-rung"
    case defaultRendition = "default-rendition"
    case nothingPlays = "nothing-plays"
    /// A reason from a newer publisher. Shown as a reason rather than dropped:
    /// the panel's job is to report, and "something this build has no word for"
    /// is itself worth reading.
    case unknown

    public init(from decoder: any Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = PlaybackReason(rawValue: raw) ?? .unknown
    }

    /// The clause that follows the headline.
    public var sentence: String {
        switch self {
        case .audioOnly: "audio-only mode — the video track is never fetched"
        case .codecsUnknown: "the server reported no stream list, so nothing was gated"
        case .archiveDecodes: "this device decodes the archived file, and quality is Auto"
        case .archiveIsEnough: "the quality asked for is at or above the source, so the archive is already it"
        case .qualityPicked: "a quality was asked for, and a rung matched it"
        case .noDecoder: "this device has no decoder for the archived file"
        case .noRung: "no rung matched, and the archived file plays here"
        case .defaultRendition: "this server offers no ladder, only the default rendition"
        case .nothingPlays: "no decoder here, and no rendition to fall back to"
        case .unknown: "reported by the player in a word this build does not know"
        }
    }
}

/// How the picture is being delivered.
public enum DeliveryKind: String, Codable, Sendable, Hashable {
    case direct
    case rendition
    case audio
    case none
    case unknown

    public init(from decoder: any Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = DeliveryKind(rawValue: raw) ?? .unknown
    }

    /// The headline. "Direct play" and "Transcoded" are the two answers worth
    /// reading at a glance: a video quietly costing the server an encode should
    /// not look like one that costs it nothing.
    public var label: String {
        switch self {
        case .direct: "Direct play"
        case .rendition: "Transcoded"
        case .audio: "Audio only"
        case .none: "Not playing"
        case .unknown: "Unknown"
        }
    }
}

/// One player's readings, whole.
///
/// Everything is defaulted and decoded tolerantly, like every other model that
/// crosses the wire: a phone reading a television running a newer build must
/// see the session rather than fail the poll over a key it does not have.
public struct PlaybackStats: Codable, Sendable, Hashable {
    public var delivery: Delivery
    public var derived: Derived
    public var player: PlayerReadings
    public var device: DeviceReadings

    public init(
        delivery: Delivery = Delivery(),
        derived: Derived = Derived(),
        player: PlayerReadings = PlayerReadings(),
        device: DeviceReadings = DeviceReadings()
    ) {
        self.delivery = delivery
        self.derived = derived
        self.player = player
        self.device = device
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        delivery = try c.decode(.delivery, or: Delivery())
        derived = try c.decode(.derived, or: Derived())
        player = try c.decode(.player, or: PlayerReadings())
        device = try c.decode(.device, or: DeviceReadings())
    }

    // MARK: - Delivery

    /// What is on screen and how it got there.
    public struct Delivery: Codable, Sendable, Hashable {
        public var kind: DeliveryKind
        public var reason: PlaybackReason
        /// The archived file: the tallest video stream the server listed.
        public var sourceHeight: Int
        public var sourceCodec: String
        /// The rung being played, when one is. `nil` for a direct play.
        public var rendition: Rendition?
        /// What the player was actually handed.
        public var url: String

        public init(
            kind: DeliveryKind = .unknown,
            reason: PlaybackReason = .unknown,
            sourceHeight: Int = 0,
            sourceCodec: String = "",
            rendition: Rendition? = nil,
            url: String = ""
        ) {
            self.kind = kind
            self.reason = reason
            self.sourceHeight = sourceHeight
            self.sourceCodec = sourceCodec
            self.rendition = rendition
            self.url = url
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            kind = try c.decode(.kind, or: DeliveryKind.unknown)
            reason = try c.decode(.reason, or: PlaybackReason.unknown)
            sourceHeight = try c.decode(.sourceHeight, or: 0)
            sourceCodec = try c.decode(.sourceCodec, or: "")
            rendition = try c.decodeIfPresent(Rendition.self, forKey: .rendition)
            url = try c.decode(.url, or: "")
        }

        /// `1080p · vp09.00.50.08`, or as much of it as is known.
        public var source: String { PlaybackStats.stream(height: sourceHeight, codec: sourceCodec) }
    }

    /// The rung being played, and how much of it exists.
    public struct Rendition: Codable, Sendable, Hashable {
        public var height: Int
        public var codec: String
        public var state: HLSState
        /// How much of the rendition has been encoded, 0…1 — which is not
        /// where playback is (see api.md), but how much of the work is done.
        public var progress: Double
        /// Playing has been asked for but the first segment is not there yet.
        public var preparing: Bool

        public init(
            height: Int = 0,
            codec: String = "",
            state: HLSState = .unknown,
            progress: Double = 0,
            preparing: Bool = false
        ) {
            self.height = height
            self.codec = codec
            self.state = state
            self.progress = progress
            self.preparing = preparing
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            height = try c.decode(.height, or: 0)
            codec = try c.decode(.codec, or: "")
            state = try c.decode(.state, or: HLSState.unknown)
            progress = try c.decode(.progress, or: 0)
            preparing = try c.decode(.preparing, or: false)
        }

        public var line: String {
            [PlaybackStats.stream(height: height, codec: codec),
             PlaybackStats.state(state, progress: progress)]
                .filter { !$0.isEmpty }
                .joined(separator: " · ")
        }
    }

    // MARK: - Derived

    /// The two things the server makes on the side, and their state.
    public struct Derived: Codable, Sendable, Hashable {
        public var preview: Preview
        public var loudness: Loudness

        public init(preview: Preview = Preview(), loudness: Loudness = Loudness()) {
            self.preview = preview
            self.loudness = loudness
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            preview = try c.decode(.preview, or: Preview())
            loudness = try c.decode(.loudness, or: Loudness())
        }
    }

    /// The scrub-preview sheet.
    public struct Preview: Codable, Sendable, Hashable {
        /// Whether the server offers one at all (`preview_url`).
        public var offered: Bool
        public var tiles: Int
        /// Seconds one still stands for.
        public var every: Double
        public var width: Int
        public var height: Int
        /// How many times it has been asked for. In the panel on purpose: a
        /// sheet is one decode of the whole file and can queue behind other
        /// work, so "waiting, asked 6×" is a wait and the same line with a
        /// failure is a bug.
        public var asked: Int

        public init(
            offered: Bool = false,
            tiles: Int = 0,
            every: Double = 0,
            width: Int = 0,
            height: Int = 0,
            asked: Int = 0
        ) {
            self.offered = offered
            self.tiles = tiles
            self.every = every
            self.width = width
            self.height = height
            self.asked = asked
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            offered = try c.decode(.offered, or: false)
            tiles = try c.decode(.tiles, or: 0)
            every = try c.decode(.every, or: 0)
            width = try c.decode(.width, or: 0)
            height = try c.decode(.height, or: 0)
            asked = try c.decode(.asked, or: 0)
        }

        public var line: String {
            if !offered { return "not offered by this server" }
            if tiles > 0 {
                return "ready · \(tiles) stills, \(width)×\(height), every \(String(format: "%.1f", every))s"
            }
            if asked == 0 { return "not asked for yet" }
            return "waiting · asked \(asked)×"
        }
    }

    /// Loudness normalisation, as it reaches the ear.
    public struct Loudness: Codable, Sendable, Hashable {
        /// The preference. A measurement that exists while this is off changes
        /// nothing about what you hear, which is why the line leads with it.
        public var enabled: Bool
        public var info: LoudnessInfo?

        public init(enabled: Bool = false, info: LoudnessInfo? = nil) {
            self.enabled = enabled
            self.info = info
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            enabled = try c.decode(.enabled, or: false)
            info = try c.decodeIfPresent(LoudnessInfo.self, forKey: .info)
        }

        public var line: String {
            if !enabled { return "off — playing at the archived level" }
            guard let info else { return "waiting" }
            guard info.state == .done else { return PlaybackStats.state(info.state, progress: nil) }
            let applied = info.gainDB < 0 ? String(format: "%.1f dB", info.gainDB) : "no change"
            return applied
                + String(format: " · measured %.1f LUFS, peak %.1f dBTP", info.measuredLUFS, info.peakDBTP)
        }
    }

    // MARK: - The player itself

    /// `AVPlayerItem`'s own counters — the web panel's "Element" group.
    public struct PlayerReadings: Codable, Sendable, Hashable {
        /// `unknown` / `ready to play` / `failed`, and whether it expects to
        /// keep up.
        public var status: String
        public var likelyToKeepUp: Bool
        public var pictureWidth: Int
        public var pictureHeight: Int
        /// Seconds of contiguous buffer ahead of the playhead. Only the range
        /// the playhead is *in* counts: a player that has loaded two minutes
        /// somewhere else on the timeline has nothing to play, and a number
        /// that says otherwise is worse than no number.
        public var bufferAhead: Double?
        public var droppedFrames: Int?
        /// The throughput the item has actually seen, bits per second.
        public var observedBitrate: Double?
        public var position: Double
        public var duration: Double
        /// Where this playback began — the resume point, or 0.
        public var startedAt: Double
        public var volume: Double
        public var muted: Bool

        public init(
            status: String = "",
            likelyToKeepUp: Bool = false,
            pictureWidth: Int = 0,
            pictureHeight: Int = 0,
            bufferAhead: Double? = nil,
            droppedFrames: Int? = nil,
            observedBitrate: Double? = nil,
            position: Double = 0,
            duration: Double = 0,
            startedAt: Double = 0,
            volume: Double = 1,
            muted: Bool = false
        ) {
            self.status = status
            self.likelyToKeepUp = likelyToKeepUp
            self.pictureWidth = pictureWidth
            self.pictureHeight = pictureHeight
            self.bufferAhead = bufferAhead
            self.droppedFrames = droppedFrames
            self.observedBitrate = observedBitrate
            self.position = position
            self.duration = duration
            self.startedAt = startedAt
            self.volume = volume
            self.muted = muted
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            status = try c.decode(.status, or: "")
            likelyToKeepUp = try c.decode(.likelyToKeepUp, or: false)
            pictureWidth = try c.decode(.pictureWidth, or: 0)
            pictureHeight = try c.decode(.pictureHeight, or: 0)
            bufferAhead = try c.decodeIfPresent(Double.self, forKey: .bufferAhead)
            droppedFrames = try c.decodeIfPresent(Int.self, forKey: .droppedFrames)
            observedBitrate = try c.decodeIfPresent(Double.self, forKey: .observedBitrate)
            position = try c.decode(.position, or: 0)
            duration = try c.decode(.duration, or: 0)
            startedAt = try c.decode(.startedAt, or: 0)
            volume = try c.decode(.volume, or: 1)
            muted = try c.decode(.muted, or: false)
        }

        /// `1280×720`, or what the item will admit to before it has a frame.
        public var picture: String {
            pictureWidth > 0 ? "\(pictureWidth)×\(pictureHeight)" : "no picture yet"
        }

        public var state: String {
            status.isEmpty ? "—" : status + (likelyToKeepUp ? " · keeping up" : " · not keeping up")
        }
    }

    /// The screen the video is on.
    public struct DeviceReadings: Codable, Sendable, Hashable {
        /// The codecs this device admits to decoding in hardware.
        public var decoders: [String]
        public var screenHeight: Int

        public init(decoders: [String] = [], screenHeight: Int = 0) {
            self.decoders = decoders
            self.screenHeight = screenHeight
        }

        public init(from decoder: any Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            decoders = try c.decode(.decoders, or: [])
            screenHeight = try c.decode(.screenHeight, or: 0)
        }

        public var decoderList: String { decoders.isEmpty ? "none" : decoders.joined(separator: ", ") }
    }

    // MARK: - Shared vocabulary

    /// `1080p · avc1.640028`, or as much of it as is known.
    public static func stream(height: Int, codec: String) -> String {
        var parts: [String] = []
        if height > 0 { parts.append("\(height)p") }
        if !codec.isEmpty { parts.append(codec) }
        return parts.isEmpty ? "unknown" : parts.joined(separator: " · ")
    }

    /// A derivation's state as a person would say it, with how far it has got
    /// attached.
    ///
    /// The percentage is only ever shown *while* something is running: on a
    /// finished job it is 100 by definition and says nothing, and on one that
    /// has not started it would be a zero a reader takes for a stall. Every
    /// derivation says it this way, so "deriving · 42%" means the same thing
    /// whether it came from a transcode counting segments or a scan reading
    /// ffmpeg's clock.
    public static func state(_ state: HLSState?, progress: Double?) -> String {
        let label: String
        switch state {
        case .done: label = "ready"
        case .running: label = "deriving"
        case .failed: label = "failed"
        case .pending: label = "not started"
        default: label = "waiting"
        }
        guard state == .running, let progress, progress > 0 else { return label }
        return "\(label) · \(Int((progress * 100).rounded()))%"
    }

    /// The dropped-frame count, or nil when nothing has been counted.
    ///
    /// A bare count rather than the web's `12 of 3,410 (0.4%)`: the access log
    /// reports frames dropped and never a total, and a percentage of a
    /// denominator this side invented would be exactly the kind of derived
    /// number the panel is not allowed to show.
    public static func dropped(_ dropped: Int?) -> String? {
        guard let dropped, dropped > 0 else { return nil }
        return dropped.formatted()
    }

    /// `4.8 Mbps`, the units a throughput is read in.
    public static func bitrate(_ bitsPerSecond: Double?) -> String? {
        guard let bitsPerSecond, bitsPerSecond > 0 else { return nil }
        return String(format: "%.1f Mbps", bitsPerSecond / 1_000_000)
    }
}
