import Foundation
#if canImport(UIKit)
import UIKit
#endif
#if canImport(VideoToolbox)
import VideoToolbox
#endif

/// What *this* device can do with the quality ladder: how tall a picture it can
/// actually show, and whether it decodes the HEVC rungs.
///
/// It is a value rather than a lookup so the resolution rule in ``CodecGate``
/// can be tested for a 4K TV, a phone and an old device without running on one.
public struct DeviceCapabilities: Sendable, Hashable {
    /// The screen's vertical resolution in pixels — 2160 on a 4K Apple TV,
    /// 1080 on an HD one, ~1200 on a current phone.
    public let screenHeight: Int
    /// `VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC)`. False rules the
    /// 1440 and 2160 rungs out entirely: they are HEVC and nothing else.
    public let decodesHEVC: Bool
    /// Hardware AV1 decode (A17 Pro / M3 and later). Decides whether an AV1
    /// archive plays as-is.
    public let decodesAV1: Bool
    /// Hardware VP9 decode. Same role for VP9 archives.
    public let decodesVP9: Bool

    public init(screenHeight: Int, decodesHEVC: Bool, decodesAV1: Bool = false, decodesVP9: Bool = false) {
        self.screenHeight = screenHeight
        self.decodesHEVC = decodesHEVC
        self.decodesAV1 = decodesAV1
        self.decodesVP9 = decodesVP9
    }

    /// Whether the archived stream itself decodes here: always for H.264 and
    /// HEVC, and per the hardware for AV1 and VP9. Pure, so the gate can be
    /// tested for any device without running on one.
    public func canDecode(_ stream: MediaStream) -> Bool {
        if stream.isNativelyPlayable { return true }
        guard stream.type == .video else { return false }
        let codec = stream.codec
        if codec.hasPrefix("av01") || codec.hasPrefix("av1") { return decodesAV1 }
        if codec.hasPrefix("vp09") || codec.hasPrefix("vp9") { return decodesVP9 }
        return false
    }

    /// This device, right now.
    @MainActor
    public static var current: DeviceCapabilities {
        DeviceCapabilities(
            screenHeight: DeviceScreen.pixelHeight,
            decodesHEVC: DeviceCodecs.decodesHEVC,
            decodesAV1: DeviceCodecs.hardwareDecodes("av01"),
            decodesVP9: DeviceCodecs.hardwareDecodes("vp09")
        )
    }

    /// Whether a rendition in this codec can play here. An unrecognised codec
    /// is left in rather than dropped: a rung added to the contract later is
    /// AVFoundation's problem to refuse, not something to hide from the picker.
    public func canPlay(_ codec: HLSCodec) -> Bool {
        codec == .hevc ? decodesHEVC : true
    }
}

/// The screen's real pixel height.
///
/// `nativeBounds` is in portrait orientation, so the *shorter* side is the one
/// a 16:9 video fills top to bottom when it is on screen: 2160 on a 4K TV,
/// 1080 on an HD one, 1206 on an iPhone 17. The screen is reached through the
/// active window scene because `UIScreen.main` is deprecated from iOS 26 and
/// tvOS 26 on.
@MainActor
public enum DeviceScreen {
    /// What to assume when there is no scene to ask — a unit test host, or a
    /// launch before any window exists. 1080p is the rung every video offers.
    public static let fallbackHeight = 1080

    public static var pixelHeight: Int {
        #if canImport(UIKit)
        let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
        let scene = scenes.first { $0.activationState == .foregroundActive } ?? scenes.first
        guard let bounds = scene?.screen.nativeBounds, bounds.height > 0, bounds.width > 0 else {
            return fallbackHeight
        }
        return Int(min(bounds.width, bounds.height))
        #else
        return fallbackHeight
        #endif
    }
}
