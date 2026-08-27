import Foundation
#if canImport(VideoToolbox)
import VideoToolbox
#endif

/// What *this* device can decode beyond the codecs AVFoundation always
/// handles. H.264 and HEVC play everywhere; VP9 and AV1 depend on the chip
/// (AV1 arrived with the A17 Pro / M3), so the answer has to come from
/// VideoToolbox at runtime rather than from a static list.
public enum DeviceCodecs {
    /// True when the archived stream's codec is decodable here, either
    /// because AVFoundation always can or because the hardware says so.
    public static func canDecode(_ stream: MediaStream) -> Bool {
        if stream.isNativelyPlayable { return true }
        guard stream.type == .video else { return false }
        return hardwareDecodes(stream.codec)
    }

    static func hardwareDecodes(_ codec: String) -> Bool {
        #if canImport(VideoToolbox)
        if codec.hasPrefix("av01") || codec.hasPrefix("av1") {
            return VTIsHardwareDecodeSupported(kCMVideoCodecType_AV1)
        }
        if codec.hasPrefix("vp09") || codec.hasPrefix("vp9") {
            return VTIsHardwareDecodeSupported(kCMVideoCodecType_VP9)
        }
        #endif
        return false
    }
}
