import Foundation

/// What another device should open to carry on — the payload of a Handoff
/// activity, and nothing else.
///
/// One activity type carries every kind of continuation. "Continue what I was
/// doing" is one question, and two types would mean two entries in both apps'
/// `NSUserActivityTypes` and two handlers on the receiving side to answer it.
///
/// The watch case is deliberately the same shape as a web link: the context
/// travels as ``PlaybackContext/queryItems``, which is what makes
/// ``webpageURL`` a URL the web client already understands and a Mac with no
/// Flimm installed a perfectly good destination for a handoff.
public struct Continuation: Hashable, Sendable {
    /// Where playback or browsing was.
    public enum Destination: Hashable, Sendable {
        /// A video at a position, in the run it is being played from.
        case watch(videoId: String, position: Double, context: PlaybackContext)
        /// A screen. Nothing else about it — a scroll offset is not something
        /// another device can honour, and pretending otherwise lands the
        /// viewer somewhere they did not leave.
        case page(Page)
    }

    /// The screens worth continuing: the ones that have an address of their
    /// own in the web client, which is also what ``webpageURL`` needs.
    ///
    /// A search is not among them. The query lives in each screen's own
    /// search field rather than in the navigation state, so there is nothing
    /// here that could be restored without inventing it.
    public enum Page: Hashable, Sendable {
        case feed(String)
        case channel(String)
        case playlist(String)
        case history
        case stats
    }

    /// Reverse-DNS, and named in `NSUserActivityTypes` in **both** apps'
    /// Info.plists. The phone and the Apple TV must carry the same string or
    /// neither sees the other's activity.
    public static let activityType = "dev.winktech.flimm.continue"

    /// Which Flimm this came from. The app is multi-server: a phone signed in
    /// to another deployment cannot open any of these ids, and a receiver that
    /// checks first can say so instead of 404ing its way through them.
    public var server: URL
    public var destination: Destination
    /// What the Handoff banner reads — the video's title, or the screen's.
    public var title: String

    public init(server: URL, destination: Destination, title: String) {
        self.server = server
        self.destination = destination
        self.title = title
    }

    // MARK: - The wire

    /// `NSUserActivity.userInfo`, as a flat dictionary of strings.
    ///
    /// Strings throughout because a user activity's payload has to be
    /// property-list types, and one uniform kind of value is one less thing to
    /// get wrong on the way back in. It stays small on purpose — Handoff
    /// broadcasts this to nearby devices, and none of it is worth a kilobyte.
    ///
    /// A media token must never be put here. This dictionary leaves the device.
    public var userInfo: [String: String] {
        var info = ["server": server.absoluteString, "title": title]
        switch destination {
        case .watch(let videoId, let position, let context):
            info["kind"] = "watch"
            info["video"] = videoId
            info["t"] = String(Int(position.rounded()))
            // The web client's own parameter names, in its own spelling.
            for item in context.queryItems {
                guard let value = item.value else { continue }
                info[item.name] = value
            }
        case .page(let page):
            info["kind"] = "page"
            info["page"] = page.token
        }
        return info
    }

    /// Reads back what ``userInfo`` wrote. `nil` for anything this version
    /// does not understand, which is what a newer app handing off a page this
    /// one has never heard of looks like.
    public init?(userInfo: [AnyHashable: Any]) {
        func string(_ key: String) -> String? {
            guard let value = userInfo[key] as? String, !value.isEmpty else { return nil }
            return value
        }
        guard let raw = string("server"), let server = URL(string: raw) else { return nil }
        let title = string("title") ?? ""
        switch string("kind") {
        case "watch":
            guard let video = string("video") else { return nil }
            let items = userInfo.compactMap { key, value -> URLQueryItem? in
                guard let name = key as? String, let value = value as? String else { return nil }
                return URLQueryItem(name: name, value: value)
            }
            let position = Double(string("t") ?? "") ?? 0
            self.init(
                server: server,
                destination: .watch(videoId: video, position: position, context: PlaybackContext(queryItems: items)),
                title: title
            )
        case "page":
            guard let token = string("page"), let page = Page(token: token) else { return nil }
            self.init(server: server, destination: .page(page), title: title)
        default:
            return nil
        }
    }

    /// The same destination as a web address, for the other half of Handoff:
    /// a device with no Flimm app opens this in a browser instead, and the web
    /// client is the same client.
    public var webpageURL: URL? {
        guard var components = URLComponents(url: server, resolvingAgainstBaseURL: false) else { return nil }
        var query: [URLQueryItem] = []
        let path: String
        switch destination {
        case .watch(let videoId, let position, let context):
            path = "/watch/\(videoId)"
            query = context.queryItems
            // Rounded, and omitted at the very start: `?t=0` is a parameter
            // that says nothing, and the web client resumes by itself.
            let seconds = Int(position.rounded())
            if seconds > 0 { query.append(URLQueryItem(name: "t", value: String(seconds))) }
        case .page(let page):
            path = page.path
        }
        // A server reached on a sub-path keeps it; `/watch/…` is relative to
        // wherever the app itself is served from.
        components.path = components.path.hasSuffix("/") ? String(components.path.dropLast()) + path : components.path + path
        components.queryItems = query.isEmpty ? nil : query
        return components.url
    }

    /// Whether this continuation belongs to the server the receiving device is
    /// signed in to.
    ///
    /// Compared on the normalised address rather than `URL` equality, because
    /// the two ends may have arrived at the same server by different spellings
    /// — one with a trailing slash, one without — and a viewer with one server
    /// must never be told their own phone is a stranger.
    public func matches(server other: URL) -> Bool {
        Self.normalize(server) == Self.normalize(other)
    }

    static func normalize(_ url: URL) -> String {
        var text = url.absoluteString.lowercased()
        while text.hasSuffix("/") { text.removeLast() }
        return text
    }
}

extension Continuation.Page {
    /// `feed:<id>`, `history`, … — one string, so the whole payload stays flat.
    var token: String {
        switch self {
        case .feed(let id): "feed:\(id)"
        case .channel(let id): "channel:\(id)"
        case .playlist(let id): "playlist:\(id)"
        case .history: "history"
        case .stats: "stats"
        }
    }

    init?(token: String) {
        let parts = token.split(separator: ":", maxSplits: 1).map(String.init)
        switch (parts.first, parts.count) {
        case ("feed", 2): self = .feed(parts[1])
        case ("channel", 2): self = .channel(parts[1])
        case ("playlist", 2): self = .playlist(parts[1])
        case ("history", 1): self = .history
        case ("stats", 1): self = .stats
        default: return nil
        }
    }

    /// The web client's route for this page — `App.tsx` is the contract.
    var path: String {
        switch self {
        case .feed(let id): "/feeds/\(id)"
        case .channel(let id): "/channels/\(id)"
        case .playlist(let id): "/playlists/\(id)"
        case .history: "/history"
        case .stats: "/stats"
        }
    }
}
