import Foundation

/// Whether AVFoundation can play what was archived, decided from `streams`
/// before an `AVPlayer` is ever built.
///
/// The archive holds whatever was downloaded — often VP9 or AV1, which
/// AVFoundation decodes on some devices and not others, and Opus audio, which
/// it never decodes. A stalled player says nothing useful about any of that,
/// so the gate refuses with the codec's name and offers the audio-only path
/// when the server has a native rendition for it.
public enum CodecGate {
    /// Why a video cannot be played natively, and whether audio-only is still
    /// on the table.
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

    /// `nil` when the video plays, or when the server said nothing about its
    /// streams — an older backend must not be treated as "unplayable".
    ///
    /// `audioOnly` playback sidesteps the video track entirely, so it never
    /// produces an issue.
    public static func issue(for video: Video, audioOnly: Bool = false) -> Issue? {
        guard !audioOnly else { return nil }
        guard let streams = video.streams, !streams.isEmpty else { return nil }
        let videoStreams = streams.filter { $0.type == .video }
        guard !videoStreams.isEmpty, !videoStreams.contains(where: DeviceCodecs.canDecode) else { return nil }
        return Issue(videoCodec: videoStreams[0].codec, audioAvailable: video.nativeAudioURL != nil)
    }
}
