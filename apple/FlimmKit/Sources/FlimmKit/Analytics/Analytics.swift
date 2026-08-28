import Foundation
#if canImport(UIKit)
import UIKit
#endif

/// First-party analytics against a self-hosted Umami instance, shared by the
/// phone, iPad and Apple TV apps. Umami's `/api/send` accepts exactly the JSON
/// its web tracker would have posted, so a native app can speak it directly —
/// no SDK, no third party in the path.
///
/// Deliberately incurious. The visitor id is a random UUID minted per install
/// and forgotten when the app is deleted; nothing that identifies a video, a
/// channel, a playlist, a search term or a server ever leaves. Screens are
/// route *patterns* the same way the web client reports them — `/watch`, never
/// a video id — and events carry counts and outcomes only. Nothing here
/// touches the IDFA, so the apps need no tracking prompt.
///
/// The endpoint and website id are baked into each app at build time
/// (`UMAMI_URL` / `UMAMI_WEBSITE_ID` in `Config/Secrets.xcconfig`, from CI
/// secrets), so a build without them reports nothing at all. A Flimm server
/// running `ANALYTICS_DISABLED=true` turns reporting off for the app pointed
/// at it — see ``apply(_:)``, which ``AuthSession`` calls with the server's
/// config.
@MainActor
public enum Analytics {
    /// Screens as Umami sees them: `rawValue` is the path, ``title`` the label
    /// in the dashboard. The list is every screen either app can show; a
    /// platform that has no equivalent (tvOS has no feed editor) simply never
    /// reports that case.
    public enum Screen: String, Sendable {
        case server = "server-setup"
        case signIn = "sign-in"
        case home
        case feed = "feeds/:id"
        case feedEditor = "feeds/:id/edit"
        case channels
        case channel = "channels/:id"
        case playlists
        case playlist = "playlists/:id"
        case history
        case search
        case settings
        case watch = "watch/:id"

        public var title: String {
            switch self {
            case .server: "Server setup"
            case .signIn: "Sign in"
            case .home: "Home"
            case .feed: "Feed"
            case .feedEditor: "Edit feed"
            case .channels: "Channels"
            case .channel: "Channel"
            case .playlists: "Playlists"
            case .playlist: "Playlist"
            case .history: "History"
            case .search: "Search"
            case .settings: "Settings"
            case .watch: "Watch"
            }
        }
    }

    /// How a sign-in was completed. tvOS has no browser, so its flow is the
    /// device authorization grant; the split is the one thing worth knowing
    /// about sign-in.
    public enum SignInMethod: String, Sendable {
        case browser
        case deviceCode = "device-code"
    }

    /// A long offline stretch drops the oldest events rather than growing.
    private static let queueLimit = 50
    /// SwiftUI calls `onAppear` more than once on a NavigationStack root when
    /// a pushed view pops. A screen repeating inside this window is that
    /// bounce, not a second visit.
    private static let repeatWindow: TimeInterval = 2

    private static var endpoint: URL?
    private static var websiteID = ""
    private static var session: URLSession = .shared
    private static var userAgent = ""
    private static var currentPath = "/"
    private static var currentTitle = ""
    private static var lastScreenAt: Date?
    private static var pending: [[String: Any]] = []
    private static var isSending = false
    private static var cachedScreenSize: String?
    /// The video ``play(videoID:kind:audioOnly:)`` last reported.
    private static var lastPlayed: String?

    /// Random per install, kept so returning visitors are countable, and gone
    /// with the app. Never the account, never the `sub` from the token. A UUID
    /// string is well inside Umami's 50-character limit for this field.
    private static let visitorID: String = {
        let key = "analytics.visitorId"
        if let existing = UserDefaults.standard.string(forKey: key) { return existing }
        let fresh = UUID().uuidString
        UserDefaults.standard.set(fresh, forKey: key)
        return fresh
    }()

    // MARK: - Configuration

    /// A no-op unless both values are baked in — CI and simulator builds run
    /// from the placeholder xcconfig, exactly as they do for Sentry's DSN.
    public static func configure(bundle: Bundle = .main) {
        guard let raw = bundle.object(forInfoDictionaryKey: "UMAMI_URL") as? String,
              let base = URL(string: raw), base.host != nil,
              let website = bundle.object(forInfoDictionaryKey: "UMAMI_WEBSITE_ID") as? String,
              !website.isEmpty else { return }
        configure(endpoint: base, websiteID: website)
    }

    /// The configuration the bundle path ends in. Also how tests point the
    /// queue at a stubbed session.
    static func configure(endpoint base: URL, websiteID website: String, session urlSession: URLSession = .shared) {
        endpoint = base.appendingPathComponent("api/send")
        websiteID = website
        session = urlSession
        userAgent = browserUserAgent()
        #if canImport(UIKit) && !os(macOS)
        // Anything stranded by a dead network gets another chance on return.
        NotificationCenter.default.addObserver(
            forName: UIApplication.willEnterForegroundNotification, object: nil, queue: .main
        ) { _ in
            Task { @MainActor in send() }
        }
        #endif
    }

    /// The server's word on analytics. A deployment running
    /// `ANALYTICS_DISABLED=true` is never reported on, whatever this build was
    /// compiled with — including whatever was already queued for it.
    public static func apply(_ config: ServerConfig) {
        guard config.analyticsDisabled else { return }
        endpoint = nil
        pending = []
    }

    // MARK: - Reporting

    /// A pageview. Also fixes the path that subsequent ``track(_:_:)`` calls
    /// carry, so an event is filed against the screen it happened on.
    public static func screen(_ screen: Screen) {
        let path = "/\(screen.rawValue)"
        // Deliberately not "same as last" alone: a genuine second visit to the
        // screen you were just on is still worth counting, minutes later.
        if path == currentPath, let lastScreenAt, Date().timeIntervalSince(lastScreenAt) < repeatWindow { return }
        currentPath = path
        currentTitle = screen.title
        lastScreenAt = Date()
        queue(name: nil, data: [:])
    }

    /// An action on the current screen. Keep `data` free of anything
    /// identifying: counts and outcomes, never ids, titles or queries.
    public static func track(_ event: String, _ data: [String: String] = [:]) {
        queue(name: event, data: data)
    }

    // The shared event vocabulary, mirrored by the web client's
    // `lib/analytics.ts`. Clients call these rather than composing payloads,
    // so the three platforms cannot drift into three spellings of "play".

    /// Playback actually started. Reported once per video however many times
    /// a client re-enters playback for it — an audio-only toggle and a quality
    /// switch both start the same video again. Going back to a video played
    /// earlier *is* a second play, which is why this remembers the last one
    /// rather than every one.
    public static func play(videoID: String, kind: String, audioOnly: Bool) {
        guard lastPlayed != videoID else { return }
        lastPlayed = videoID
        track("play", ["kind": kind, "audio": audioOnly ? "yes" : "no"])
    }

    /// A search was committed. The scope only — never the query.
    public static func search(scope: String) {
        track("search", ["scope": scope])
    }

    /// A feed was created (not every save: edits are not this event).
    public static func feedCreated() {
        track("feed-created")
    }

    /// A sign-in completed.
    public static func signedIn(_ method: SignInMethod) {
        track("sign-in", ["method": method.rawValue])
    }

    // MARK: - Delivery

    private static func queue(name: String?, data: [String: String]) {
        guard endpoint != nil else { return }
        var payload: [String: Any] = [
            "website": websiteID,
            "hostname": hostname,
            "url": currentPath,
            "title": currentTitle,
            "language": Locale.preferredLanguages.first ?? "en-US",
            "screen": screenSize,
            "id": visitorID,
        ]
        // An event without a name is what Umami stores as a pageview.
        if let name { payload["name"] = name }
        if !data.isEmpty { payload["data"] = data }

        pending.append(payload)
        if pending.count > queueLimit { pending.removeFirst(pending.count - queueLimit) }
        send()
    }

    /// Drains the queue one event at a time, in order; a failure leaves the
    /// event in place for the next event or the next foreground to retry.
    private static func send() {
        guard let endpoint, !isSending, let next = pending.first else { return }
        guard let body = try? JSONSerialization.data(withJSONObject: ["type": "event", "payload": next]) else {
            pending.removeFirst()
            send()
            return
        }
        isSending = true

        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue(userAgent, forHTTPHeaderField: "User-Agent")
        request.httpBody = body
        session.dataTask(with: request) { _, response, error in
            let accepted = error == nil && ((response as? HTTPURLResponse)?.statusCode ?? 500) < 400
            Task { @MainActor in
                isSending = false
                guard accepted, !pending.isEmpty else { return }
                pending.removeFirst()
                send()
            }
        }.resume()
    }

    // MARK: - What the payload says about this device

    /// The "domain" this app's traffic is filed under. Not a real host — it
    /// keeps the three clients apart inside one Umami website, the way the web
    /// client is separated by the deployment's own hostname.
    private static var hostname: String {
        #if os(tvOS)
        return "flimm.tvos"
        #elseif os(iOS)
        return UIDevice.current.userInterfaceIdiom == .pad ? "flimm.ipados" : "flimm.ios"
        #else
        return "flimm.app"
        #endif
    }

    /// Umami rejects a request with no User-Agent outright, and answers one
    /// its bot filter matches with a 200 that stores nothing — so this has to
    /// read as a browser. It is also where Umami reads the OS and device from.
    private static func browserUserAgent() -> String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
        #if canImport(UIKit)
        let os = UIDevice.current.systemVersion.replacingOccurrences(of: ".", with: "_")
        #else
        let os = "17_0"
        #endif
        #if os(tvOS)
        return "Mozilla/5.0 (Apple TV; CPU OS \(os) like Mac OS X) "
            + "AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15 Flimm/\(version)"
        #else
        let device = hostname == "flimm.ipados" ? "iPad; CPU OS" : "iPhone; CPU iPhone OS"
        return "Mozilla/5.0 (\(device) \(os) like Mac OS X) "
            + "AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 Flimm/\(version)"
        #endif
    }

    /// Points, mirroring the CSS pixels the web tracker reports. Read lazily:
    /// at app init there is no window scene to measure yet.
    private static var screenSize: String {
        if let cachedScreenSize { return cachedScreenSize }
        #if canImport(UIKit) && !os(macOS)
        let size = UIApplication.shared.connectedScenes
            .compactMap { ($0 as? UIWindowScene)?.windows.first?.bounds.size }
            .first ?? .zero
        #else
        let size = CGSize.zero
        #endif
        let value = "\(Int(size.width))x\(Int(size.height))"
        if size != .zero { cachedScreenSize = value }
        return value
    }

    /// Exposed for tests, which need each case to start from nothing.
    static func reset() {
        endpoint = nil
        websiteID = ""
        session = .shared
        pending = []
        isSending = false
        currentPath = "/"
        currentTitle = ""
        lastScreenAt = nil
        lastPlayed = nil
    }
}
