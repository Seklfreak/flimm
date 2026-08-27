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

    public init(screenHeight: Int, decodesHEVC: Bool) {
        self.screenHeight = screenHeight
        self.decodesHEVC = decodesHEVC
    }

    /// This device, right now.
    @MainActor
    public static var current: DeviceCapabilities {
        DeviceCapabilities(screenHeight: DeviceScreen.pixelHeight, decodesHEVC: DeviceCodecs.decodesHEVC)
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
