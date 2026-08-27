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

    /// The four outcomes, in the order a player should prefer them.
    public enum Decision: Sendable, Hashable {
        /// Play `media_url`: the archived file decodes here, and it costs the
        /// server nothing. Also the answer when the server said nothing about
        /// `streams` — an older backend must not read as "unplayable" — and
        /// when audio-only sidesteps the video track entirely.
        case native
        /// The device has no decoder for what was archived, but the server
        /// offers the compatible rendition at this path (`hls_url`). Playing
        /// it is a real transcode of someone's CPU, which is why it is never
        /// the first choice.
        case hls(String)
        /// No compatible rendition (an older backend), but the derived AAC
        /// audio is there, so audio-only is still worth offering.
        case audioOnly(Issue)
        /// Nothing plays here: no decoder, no compatible rendition, no
        /// derived audio.
        case unplayable(Issue)
    }

    /// The gate. `audioOnly` playback never touches the video track, so it is
    /// always ``Decision/native``.
    public static func decision(for video: Video, audioOnly: Bool = false) -> Decision {
        guard !audioOnly else { return .native }
        guard let streams = video.streams, !streams.isEmpty else { return .native }
        let videoStreams = streams.filter { $0.type == .video }
        guard !videoStreams.isEmpty, !videoStreams.contains(where: DeviceCodecs.canDecode) else { return .native }
        if let compatible = video.compatibleVideoURL { return .hls(compatible) }
        let issue = Issue(videoCodec: videoStreams[0].codec, audioAvailable: video.nativeAudioURL != nil)
        return issue.audioAvailable ? .audioOnly(issue) : .unplayable(issue)
    }
}
