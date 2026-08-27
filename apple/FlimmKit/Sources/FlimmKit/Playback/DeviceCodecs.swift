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

    /// HEVC is what the 1440 and 2160 rungs of the HLS ladder are encoded in.
    /// Every device that can drive a 4K panel decodes it in hardware — the
    /// iPhone 7 and the first Apple TV 4K onwards — but an older one does not,
    /// and it must be offered the H.264 rungs instead of a black picture.
    public static var decodesHEVC: Bool {
        #if canImport(VideoToolbox)
        return VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC)
        #else
        return false
        #endif
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
