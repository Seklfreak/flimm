import Foundation

/// Which rendition to play, as the viewer asked for it on *this* device.
///
/// It is deliberately not a server preference: quality is a property of the
/// screen and the network in front of it, and an Apple TV on ethernet wants a
/// different answer from a phone on cellular. ``CodecGate`` resolves it against
/// the video's `hls_variants` at play time; see ``PlaybackSettings`` for where
/// it is kept.
public enum QualityPreference: RawRepresentable, Codable, Sendable, Hashable, Identifiable {
    /// The archived file when this device can decode it — full quality and no
    /// transcode — and otherwise the tallest rendition the screen can show.
    case auto
    /// A rendition of this height, or the nearest lower one the video offers.
    /// Asked for explicitly it wins even over a playable archive: choosing
    /// 720p on a 4K source is a request for less data, not a mistake.
    case height(Int)

    /// The rungs the ladder can hold, tallest first (`docs/api.md`). A video
    /// offers the ones at or below its source height; the settings screens
    /// offer all of them, because the choice is per device, not per video.
    public static let heights = [2160, 1440, 1080, 720, 480]

    /// What a settings screen lists.
    public static let options: [QualityPreference] = [.auto] + heights.map(QualityPreference.height)

    public init?(rawValue: String) {
        if rawValue == "auto" {
            self = .auto
            return
        }
        guard let height = Int(rawValue), height > 0 else { return nil }
        self = .height(height)
    }

    public var rawValue: String {
        switch self {
        case .auto: "auto"
        case .height(let height): String(height)
        }
    }

    public var id: String { rawValue }

    /// The height asked for, or `nil` for ``auto``.
    public var height: Int? {
        switch self {
        case .auto: nil
        case .height(let height): height
        }
    }
}
