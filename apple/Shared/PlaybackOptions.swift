import FlimmKit
import Foundation

/// The speeds every client offers, in the order they step through with
/// `,`/`.` on a keyboard and the transport bar's rate control on a TV.
enum PlaybackSpeeds {
    static let all: [Double] = [0.75, 1.0, 1.25, 1.5, 1.75, 2.0]
}

enum SubtitleLanguages {
    /// A short list for the picker; a code the server already holds that is not
    /// here is added to the picker at runtime rather than being lost.
    static let common = ["en", "de", "es", "fr", "it", "nl", "pt", "pl", "ru", "ja", "ko", "zh"]
}

/// How both apps name what the quality picker offers.
///
/// The rule itself is ``CodecGate``'s; this is only the wording, kept in one
/// place so the phone's menu, the TV's Info panel and the two settings screens
/// say the same things.
enum VideoQuality {
    /// What a settings screen lists: `Auto`, then every rung the contract has.
    static let options = QualityPreference.options

    /// `Auto` / `1080p`.
    static func label(_ preference: QualityPreference) -> String {
        switch preference {
        case .auto: "Auto"
        case .height(let height): "\(height)p"
        }
    }

    /// `1080p` / `2160p · HEVC` — the codec is worth saying only where it is
    /// the reason a device might not be offered the rung.
    static func label(_ variant: HLSVariant) -> String {
        variant.codec == .hevc ? "\(variant.height)p · HEVC" : "\(variant.height)p"
    }

    /// A rendition's state in one word, and nothing at all while nobody has
    /// asked for it — "pending" is the normal state of most of the ladder and
    /// says nothing a viewer can act on.
    static func stateHint(_ state: HLSState?) -> String? {
        switch state {
        case .done: "ready"
        case .running: "preparing"
        case .pending, .failed, .unknown, nil: nil
        }
    }

    /// What Auto plays when the archive decodes here: `Source · 2160p · AV1`.
    static func sourceLabel(for video: Video?) -> String {
        var parts = ["Source"]
        if let height = video?.height, height > 0 { parts.append("\(height)p") }
        if let codec = video?.streams?.first(where: { $0.type == .video })?.codec, !codec.isEmpty {
            parts.append(codecName(codec))
        }
        return parts.joined(separator: " · ")
    }

    /// `av01.0.05M.08` → `AV1`. The archive's codec strings are RFC 6381
    /// identifiers, which no viewer should have to read.
    static func codecName(_ codec: String) -> String {
        let known = [
            ("avc", "H.264"), ("hvc1", "HEVC"), ("hev1", "HEVC"),
            ("av01", "AV1"), ("av1", "AV1"), ("vp09", "VP9"), ("vp9", "VP9"), ("vp08", "VP8"), ("vp8", "VP8")
        ]
        if let match = known.first(where: { codec.hasPrefix($0.0) }) { return match.1 }
        return (codec.split(separator: ".").first.map(String.init) ?? codec).uppercased()
    }

    /// The rendition a player is on, as the "compatible version" hint puts it:
    /// `1080p · compatible version`, or just the tail on a server that has no
    /// ladder to name a height from.
    static func renditionHint(_ variant: HLSVariant?) -> String {
        guard let variant else { return "compatible version" }
        return "\(label(variant)) · compatible version"
    }
}
