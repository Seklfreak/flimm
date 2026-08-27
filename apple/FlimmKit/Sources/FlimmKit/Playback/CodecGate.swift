import Foundation

/// What to play, decided from `streams` before an `AVPlayer` is ever built.
///
/// The archive holds whatever was downloaded — often VP9 or AV1, which
/// AVFoundation decodes on some devices and not others, and Opus audio, which
/// it never decodes. A stalled player says nothing useful about any of that,
/// so the decision is made up front: play the archived file when the device
/// can decode it, and otherwise the server's compatible H.264/AAC rendition.
/// Only a server without that rendition leaves a video with nowhere to go, and
/// only then does the gate refuse by name.
public enum CodecGate {
    /// Why a video cannot be played at all, and whether audio-only is still
    /// on the table. Reached only on a backend that predates `hls_url`.
    public struct Issue: Sendable, Hashable {
        /// The codec to name in the message, e.g. `vp09`.
        public let videoCodec: String
        /// True when the server offers ``Video/nativeAudioURL`` for this
        /// video — absent on a backend that predates `audio_aac_url`,
        /// regardless of the archived audio codec.
        public let audioAvailable: Bool

        public init(videoCodec: String, audioAvailable: Bool) {
            self.videoCodec = videoCodec
            self.audioAvailable = audioAvailable
        }

        public var message: String {
            "This video's codec (\(videoCodec)) can't be played on this device."
        }
    }

    /// The rendition the gate settled on: what to play, and — when the server
    /// offers a ladder — which rung it came from, so the player can start that
    /// height's job and name it on screen.
    public struct HLSChoice: Sendable, Hashable {
        /// The playlist to hand to `AVPlayer`.
        public let url: String
        /// The ladder entry this came from. `nil` on a backend that predates
        /// `hls_variants`, where `hls_url` was the only rendition there was:
        /// its height is unknown, and `POST /videos/{id}/hls` is then called
        /// without one.
        public let variant: HLSVariant?

        public init(url: String, variant: HLSVariant? = nil) {
            self.url = url
            self.variant = variant
        }

        public var height: Int? { variant?.height }
        public var state: HLSState? { variant?.state }
        public var codec: HLSCodec? { variant?.codec }
    }

    /// The four outcomes, in the order a player should prefer them.
    public enum Decision: Sendable, Hashable {
        /// Play `media_url`: the archived file decodes here, and it costs the
        /// server nothing. Also the answer when the server said nothing about
        /// `streams` — an older backend must not read as "unplayable" — and
        /// when audio-only sidesteps the video track entirely.
        case native
        /// Play a rendition instead: either because the device has no decoder
        /// for what was archived, or because the viewer asked for a specific
        /// height. It is a real transcode of someone's CPU, which is why
        /// ``QualityPreference/auto`` never chooses it over a playable archive.
        case hls(HLSChoice)
        /// No compatible rendition (an older backend), but the derived AAC
        /// audio is there, so audio-only is still worth offering.
        case audioOnly(Issue)
        /// Nothing plays here: no decoder, no compatible rendition, no
        /// derived audio.
        case unplayable(Issue)
    }

    /// The gate, and with it the quality rule.
    ///
    /// In order:
    ///
    /// 1. `audioOnly` never touches the video track, so it is always
    ///    ``Decision/native`` — as is a video the server reports no `streams`
    ///    for, which means "unknown", not "unplayable".
    /// 2. A decodable archive plus ``QualityPreference/auto`` is
    ///    ``Decision/native``: the original file, full quality, no transcode.
    /// 3. Otherwise the ladder decides — an explicit height even when the
    ///    archive would have played, because "720p" is a request for less
    ///    data. ``variant(for:in:on:)`` has the picking rule.
    /// 4. Nothing in the ladder (an older backend, or every rung in a codec
    ///    this device lacks): the archive if it plays at all, then `hls_url`,
    ///    then the wall — audio-only if the derived AAC audio is there.
    public static func decision(
        for video: Video,
        preference: QualityPreference = .auto,
        audioOnly: Bool = false,
        device: DeviceCapabilities
    ) -> Decision {
        guard !audioOnly else { return .native }
        guard let streams = video.streams, !streams.isEmpty else { return .native }
        let videoStreams = streams.filter { $0.type == .video }
        guard !videoStreams.isEmpty else { return .native }
        let archivePlays = videoStreams.contains(where: DeviceCodecs.canDecode)
        if archivePlays, preference == .auto { return .native }
        // An explicit pick at or above the source's own height buys nothing
        // over the archive when the archive plays — skip the transcode.
        if archivePlays, case .height(let h) = preference,
           let source = videoStreams.map(\.height).filter({ $0 > 0 }).max(), h >= source {
            return .native
        }
        if let picked = variant(for: preference, in: video.hlsLadder, on: device) {
            return .hls(HLSChoice(url: picked.url, variant: picked))
        }
        if archivePlays { return .native }
        if let compatible = video.compatibleVideoURL { return .hls(HLSChoice(url: compatible)) }
        let issue = Issue(videoCodec: videoStreams[0].codec, audioAvailable: video.nativeAudioURL != nil)
        return issue.audioAvailable ? .audioOnly(issue) : .unplayable(issue)
    }

    /// Whether the archived file itself decodes on this device — what makes
    /// ``QualityPreference/auto`` mean "the source, at full quality, for free".
    /// A video the server reports no `streams` for counts as playable: unknown
    /// must not read as unplayable.
    public static func archivePlays(_ video: Video) -> Bool {
        guard let streams = video.streams, !streams.isEmpty else { return true }
        let videoStreams = streams.filter { $0.type == .video }
        guard !videoStreams.isEmpty else { return true }
        return videoStreams.contains(where: DeviceCodecs.canDecode)
    }

    /// Which rung to play, out of the ones this device can decode at all.
    ///
    /// - ``QualityPreference/height(_:)``: that height, or the nearest lower
    ///   one offered — a video whose source is 1080p has no 1440 rung, and the
    ///   answer there is 1080, not a refusal.
    /// - ``QualityPreference/auto``: the tallest rung the screen can actually
    ///   show. Anything above it is bandwidth and server time spent on pixels
    ///   nobody sees.
    ///
    /// Either way, a ladder that starts above what was asked for falls to its
    /// smallest rung rather than to nothing.
    public static func variant(
        for preference: QualityPreference,
        in ladder: [HLSVariant],
        on device: DeviceCapabilities
    ) -> HLSVariant? {
        let playable = ladder
            .filter { device.canPlay($0.codec) }
            .sorted { $0.height > $1.height }
        guard !playable.isEmpty else { return nil }
        let ceiling = preference.height ?? device.screenHeight
        return playable.first { $0.height <= ceiling } ?? playable.last
    }
}
