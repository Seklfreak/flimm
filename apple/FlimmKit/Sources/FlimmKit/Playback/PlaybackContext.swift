import Foundation

/// The list the player is stepping through, plus how it is being played.
///
/// This is the player's whole state, and it must survive next/previous,
/// autoplay and a relaunch — exactly as the web client carries it in the URL.
/// ``queryItems`` produces the same parameters in the same order, so a link
/// shared between a phone and a browser resolves to the same run.
public struct PlaybackContext: Sendable, Hashable, Codable {
    /// What the list is drawn from. Mutually exclusive server-side.
    public enum Source: Sendable, Hashable, Codable {
        case feed(String)
        case playlist(String)
        case channel(String)

        var queryItem: URLQueryItem {
            switch self {
            case .feed(let id): URLQueryItem(name: "feed", value: id)
            case .playlist(let id): URLQueryItem(name: "playlist", value: id)
            case .channel(let id): URLQueryItem(name: "channel", value: id)
            }
        }
    }

    /// No context: the video was opened on its own. `nav` reports `index: -1`
    /// and the client hides the step controls.
    public static let none = PlaybackContext()

    public var source: Source?
    /// An opaque seed, not a queue. The same seed always yields the same
    /// order; a new seed reshuffles. There is no server-side shuffle state.
    public var shuffleSeed: String?
    /// Audio-only playback — `audio_url` instead of `media_url`. Seeded from a
    /// playlist's `music` flag and then carried like the seed.
    public var audioOnly: Bool

    public init(source: Source? = nil, shuffleSeed: String? = nil, audioOnly: Bool = false) {
        self.source = source
        self.shuffleSeed = shuffleSeed
        self.audioOnly = audioOnly
    }

    public static func feed(_ id: String, shuffleSeed: String? = nil, audioOnly: Bool = false) -> PlaybackContext {
        PlaybackContext(source: .feed(id), shuffleSeed: shuffleSeed, audioOnly: audioOnly)
    }

    public static func playlist(_ id: String, shuffleSeed: String? = nil, audioOnly: Bool = false) -> PlaybackContext {
        PlaybackContext(source: .playlist(id), shuffleSeed: shuffleSeed, audioOnly: audioOnly)
    }

    public static func channel(_ id: String, shuffleSeed: String? = nil, audioOnly: Bool = false) -> PlaybackContext {
        PlaybackContext(source: .channel(id), shuffleSeed: shuffleSeed, audioOnly: audioOnly)
    }

    /// The playlist being played *from*, which is what the progress heartbeat
    /// needs in order to suppress watch state for a music playlist.
    public var playlistId: String? {
        if case .playlist(let id) = source { return id }
        return nil
    }

    public var isShuffled: Bool { !(shuffleSeed?.isEmpty ?? true) }

    /// A fresh seed. Pressing "shuffle" again means picking a new one.
    public static func newShuffleSeed() -> String {
        UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
    }

    /// Query parameters for `nav`, `up-next` and any player link, in the
    /// web client's order: source first, then `shuffle`, then `audio`.
    public var queryItems: [URLQueryItem] {
        var items: [URLQueryItem] = []
        if let source { items.append(source.queryItem) }
        if let shuffleSeed, !shuffleSeed.isEmpty {
            items.append(URLQueryItem(name: "shuffle", value: shuffleSeed))
        }
        if audioOnly { items.append(URLQueryItem(name: "audio", value: "1")) }
        return items
    }

    /// Rebuilds a context from query parameters — a deep link, a restored
    /// player state. Unknown parameters are ignored.
    public init(queryItems: [URLQueryItem]) {
        var source: Source?
        var seed: String?
        var audio = false
        for item in queryItems {
            guard let value = item.value, !value.isEmpty else { continue }
            switch item.name {
            case "feed" where source == nil: source = .feed(value)
            case "playlist" where source == nil: source = .playlist(value)
            case "channel" where source == nil: source = .channel(value)
            case "shuffle": seed = value
            case "audio": audio = value == "1"
            default: continue
            }
        }
        self.init(source: source, shuffleSeed: seed, audioOnly: audio)
    }

    /// A `?…` string, empty when there is nothing to carry.
    public var queryString: String {
        var components = URLComponents()
        components.queryItems = queryItems.isEmpty ? nil : queryItems
        guard let query = components.percentEncodedQuery, !query.isEmpty else { return "" }
        return "?" + query
    }
}
