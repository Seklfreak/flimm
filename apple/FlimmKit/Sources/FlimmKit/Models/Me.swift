import Foundation

public enum SubtitleSize: String, Codable, Sendable, CaseIterable {
    case small
    case medium
    case large
}

public enum AppTheme: String, Codable, Sendable, CaseIterable {
    case system
    case light
    case dark
}

/// Server-held preferences. They follow the account across web and native, so
/// a client reads them rather than keeping its own copy.
public struct Prefs: Codable, Sendable, Hashable {
    /// Language code, or ``Prefs/subtitlesOff`` when the viewer turned
    /// subtitles off. Defaults to `en`.
    public static let subtitlesOff = "off"

    public var autoplay: Bool
    public var playbackSpeed: Double
    public var subtitleLang: String
    public var subtitleSize: SubtitleSize
    /// The master switch: off and no SponsorBlock segment acts at all.
    public var skipSponsors: Bool
    /// What each category does while ``skipSponsors`` is on. The server sends
    /// every category it knows, so a missing one is a category this build
    /// predates — treated as ``SponsorSetting/off``, never guessed at.
    public var sponsorActions: [String: SponsorSetting]
    /// Crowd-sourced titles from DeArrow, and — set separately, because they
    /// are separate things to want — crowd-sourced thumbnails. The server
    /// applies both, so a title reads the same here as on the web.
    public var dearrowTitles: DeArrowSetting
    public var dearrowThumbnails: DeArrowSetting
    /// Even out the difference between channels: the player applies the gain
    /// from `GET /videos/{id}/loudness` rather than playing every video at
    /// whatever level it was uploaded at. On unless the viewer turns it off.
    public var normalizeLoudness: Bool
    /// "Everything" is read-only as a feed, so its three options live here.
    public var everythingSort: FeedSort
    public var everythingHideSeen: Bool
    public var everythingIncludeShorts: Bool
    public var theme: AppTheme

    public init(
        autoplay: Bool = true,
        playbackSpeed: Double = 1.0,
        subtitleLang: String = "en",
        subtitleSize: SubtitleSize = .medium,
        skipSponsors: Bool = true,
        sponsorActions: [String: SponsorSetting] = SponsorSetting.defaults,
        dearrowTitles: DeArrowSetting = .off,
        dearrowThumbnails: DeArrowSetting = .off,
        normalizeLoudness: Bool = true,
        everythingSort: FeedSort = .newest,
        everythingHideSeen: Bool = true,
        everythingIncludeShorts: Bool = false,
        theme: AppTheme = .system
    ) {
        self.autoplay = autoplay
        self.playbackSpeed = playbackSpeed
        self.subtitleLang = subtitleLang
        self.subtitleSize = subtitleSize
        self.skipSponsors = skipSponsors
        self.sponsorActions = sponsorActions
        self.dearrowTitles = dearrowTitles
        self.dearrowThumbnails = dearrowThumbnails
        self.normalizeLoudness = normalizeLoudness
        self.everythingSort = everythingSort
        self.everythingHideSeen = everythingHideSeen
        self.everythingIncludeShorts = everythingIncludeShorts
        self.theme = theme
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = Prefs()
        autoplay = try c.decode(.autoplay, or: d.autoplay)
        playbackSpeed = try c.decode(.playbackSpeed, or: d.playbackSpeed)
        subtitleLang = try c.decode(.subtitleLang, or: d.subtitleLang)
        subtitleSize = try c.decode(.subtitleSize, or: d.subtitleSize)
        skipSponsors = try c.decode(.skipSponsors, or: d.skipSponsors)
        // An unknown value decodes as `off` rather than failing the whole
        // response: a category Flimm adds later must not stop the app reading
        // its own preferences.
        sponsorActions = (try? c.decodeIfPresent([String: String].self, forKey: .sponsorActions))
            .map { raw in raw?.mapValues { SponsorSetting(rawValue: $0) ?? .off } ?? d.sponsorActions }
            ?? d.sponsorActions
        dearrowTitles = try c.decode(.dearrowTitles, or: d.dearrowTitles)
        dearrowThumbnails = try c.decode(.dearrowThumbnails, or: d.dearrowThumbnails)
        normalizeLoudness = try c.decode(.normalizeLoudness, or: d.normalizeLoudness)
        everythingSort = try c.decode(.everythingSort, or: d.everythingSort)
        everythingHideSeen = try c.decode(.everythingHideSeen, or: d.everythingHideSeen)
        everythingIncludeShorts = try c.decode(.everythingIncludeShorts, or: d.everythingIncludeShorts)
        theme = try c.decode(.theme, or: d.theme)
    }
}

/// Body for `PATCH /me/prefs`. Every field is optional: only what is set is
/// sent, and the response is the full ``Prefs``.
public struct PrefsPatch: Codable, Sendable, Hashable {
    public var autoplay: Bool?
    public var playbackSpeed: Double?
    public var subtitleLang: String?
    public var subtitleSize: SubtitleSize?
    public var skipSponsors: Bool?
    public var sponsorActions: [String: SponsorSetting]?
    public var dearrowTitles: DeArrowSetting?
    public var dearrowThumbnails: DeArrowSetting?
    public var normalizeLoudness: Bool?
    public var everythingSort: FeedSort?
    public var everythingHideSeen: Bool?
    public var everythingIncludeShorts: Bool?
    public var theme: AppTheme?

    public init(
        autoplay: Bool? = nil,
        playbackSpeed: Double? = nil,
        subtitleLang: String? = nil,
        subtitleSize: SubtitleSize? = nil,
        skipSponsors: Bool? = nil,
        sponsorActions: [String: SponsorSetting]? = nil,
        dearrowTitles: DeArrowSetting? = nil,
        dearrowThumbnails: DeArrowSetting? = nil,
        normalizeLoudness: Bool? = nil,
        everythingSort: FeedSort? = nil,
        everythingHideSeen: Bool? = nil,
        everythingIncludeShorts: Bool? = nil,
        theme: AppTheme? = nil
    ) {
        self.autoplay = autoplay
        self.playbackSpeed = playbackSpeed
        self.subtitleLang = subtitleLang
        self.subtitleSize = subtitleSize
        self.skipSponsors = skipSponsors
        self.sponsorActions = sponsorActions
        self.dearrowTitles = dearrowTitles
        self.dearrowThumbnails = dearrowThumbnails
        self.normalizeLoudness = normalizeLoudness
        self.everythingSort = everythingSort
        self.everythingHideSeen = everythingHideSeen
        self.everythingIncludeShorts = everythingIncludeShorts
        self.theme = theme
    }
}

/// What a viewer has a SponsorBlock category set to.
///
/// `ask` is the middle ground the categories that are *sometimes* the point —
/// an intro, a recap — default to: the player offers a button instead of
/// jumping, because sometimes that section is what someone came for.
public enum SponsorSetting: String, Codable, Sendable, Hashable, CaseIterable {
    case skip
    case ask
    case off

    /// Mirrors the server's `defaultSponsorActions`, for a client that has to
    /// show something before `/me` answers.
    public static let defaults: [String: SponsorSetting] = [
        "sponsor": .skip, "selfpromo": .skip, "interaction": .skip,
        "intro": .ask, "outro": .ask, "preview": .ask,
        "music_offtopic": .ask, "filler": .ask, "exclusive_access": .ask,
    ]

    /// The categories a viewer can set, in the order the settings screens show
    /// them: the three that interrupt a video first, then the ones that are
    /// sometimes what they came for.
    public static let categories = [
        "sponsor", "selfpromo", "interaction",
        "intro", "outro", "preview", "filler", "music_offtopic", "exclusive_access",
    ]
}

/// What a viewer wants from DeArrow, for titles and for thumbnails
/// separately.
///
/// `manual` is what people submitted and the crowd voted on. `all` adds what
/// DeArrow generates where nobody has submitted anything: a title with the
/// shouting taken out, and a frame it picked itself. Both are applied by the
/// server, so every client shows a video under the same name.
public enum DeArrowSetting: String, Codable, Sendable, Hashable, CaseIterable {
    case off
    case manual
    case all

    public var label: String {
        switch self {
        case .off: "Off"
        case .manual: "Manual"
        case .all: "All"
        }
    }

    /// The next setting a row cycles to, for a remote that has no picker.
    public var next: DeArrowSetting {
        switch self {
        case .off: .manual
        case .manual: .all
        case .all: .off
        }
    }
}

/// `GET /me`.
public struct Me: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let email: String
    public let isAdmin: Bool
    public let prefs: Prefs

    public init(id: String, name: String = "", email: String = "", isAdmin: Bool = false, prefs: Prefs = .init()) {
        self.id = id
        self.name = name
        self.email = email
        self.isAdmin = isAdmin
        self.prefs = prefs
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(.name, or: "")
        email = try c.decode(.email, or: "")
        isAdmin = try c.decode(.isAdmin, or: false)
        prefs = try c.decode(.prefs, or: Prefs())
    }
}
