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
