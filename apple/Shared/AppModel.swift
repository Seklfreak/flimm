import FlimmKit
import Foundation
import Observation

/// Everything a signed-in screen needs: the API client, the account and its
/// preferences, and the two small lists the whole app leans on (feeds and
/// pinned playlists).
///
/// Prefs live on the server (`GET /me`, `PATCH /me/prefs`) and are the single
/// source of truth for autoplay, speed, subtitles, sponsor skipping and theme.
/// Nothing here decides *what* to show — that is the backend's job.
@MainActor
@Observable
final class AppModel {
    let client: APIClient
    /// Lists survive the shell being rebuilt by an iPad size-class flip; see
    /// ``PagerStore``.
    @ObservationIgnored let pagers = PagerStore()

    private(set) var me: Me?
    private(set) var feeds: [Feed] = []
    private(set) var pinnedPlaylists: [PlaylistSummary] = []
    private(set) var pinnedChannels: [ChannelSummary] = []
    /// Set when the first load failed; screens show it with a Retry.
    private(set) var loadError: String?
    private(set) var isLoading = false

    var prefs: Prefs { me?.prefs ?? Prefs() }

    /// The feed the app opens on: the pinned one, else the first.
    var launchFeed: Feed? {
        feeds.first { $0.pinned } ?? feeds.first
    }

    init(client: APIClient) {
        self.client = client
    }

    // MARK: - Loading

    func loadIfNeeded() async {
        guard me == nil, !isLoading else { return }
        await load()
    }

    func load() async {
        isLoading = true
        defer { isLoading = false }
        async let account = client.me()
        async let feedList = client.feeds()
        async let pinned = client.pinnedPlaylists()
        async let pinnedChans = client.pinnedChannels()
        do {
            let (loadedMe, loadedFeeds, loadedPinned, loadedChannels) = try await (account, feedList, pinned, pinnedChans)
            me = loadedMe
            feeds = loadedFeeds
            pinnedPlaylists = loadedPinned
            pinnedChannels = loadedChannels
            loadError = nil
        } catch {
            // A dropped request is not a reason to lose what we already have.
            loadError = Self.message(for: error)
        }
    }

    func refreshFeeds() async {
        guard let loaded = try? await client.feeds() else { return }
        feeds = loaded
    }

    func refreshPinnedPlaylists() async {
        guard let loaded = try? await client.pinnedPlaylists() else { return }
        pinnedPlaylists = loaded
    }

    func refreshPinnedChannels() async {
        guard let loaded = try? await client.pinnedChannels() else { return }
        pinnedChannels = loaded
    }

    /// A single video's watched *or dismissed* state just changed — an
    /// explicit "Mark seen"/"Mark unseen"/"Not interested"/"Add back", or the
    /// server's own progress heartbeat replying `watched: true` as playback
    /// reached the end. Every cached list that filters by either (an
    /// "Unseen" feed or channel, "Continue watching", any feed at all once a
    /// dismissal changes what it contains) is stale in exactly the way a bulk
    /// "Mark all seen" already accounts for, so this gets the same full wipe
    /// rather than a second, narrower invalidation path to keep in sync with
    /// it.
    func videoListStateChanged() async {
        pagers.removeAll()
        await refreshFeeds()
    }

    // MARK: - Preferences

    /// Applies a patch optimistically, then persists it. A failed write rolls
    /// the local copy back so the UI never claims a setting that did not stick.
    func updatePrefs(_ patch: PrefsPatch) async {
        let previous = me
        if let current = me {
            me = Me(
                id: current.id,
                name: current.name,
                email: current.email,
                isAdmin: current.isAdmin,
                prefs: Self.apply(patch, to: current.prefs)
            )
        }
        do {
            let saved = try await client.updatePrefs(patch)
            if let current = me {
                me = Me(id: current.id, name: current.name, email: current.email, isAdmin: current.isAdmin, prefs: saved)
            }
        } catch {
            me = previous
            loadError = Self.message(for: error)
        }
    }

    nonisolated static func apply(_ patch: PrefsPatch, to prefs: Prefs) -> Prefs {
        var next = prefs
        if let value = patch.autoplay { next.autoplay = value }
        if let value = patch.playbackSpeed { next.playbackSpeed = value }
        if let value = patch.subtitleLang { next.subtitleLang = value }
        if let value = patch.subtitleSize { next.subtitleSize = value }
        if let value = patch.skipSponsors { next.skipSponsors = value }
        if let value = patch.normalizeLoudness { next.normalizeLoudness = value }
        if let value = patch.everythingSort { next.everythingSort = value }
        if let value = patch.everythingHideSeen { next.everythingHideSeen = value }
        if let value = patch.everythingIncludeShorts { next.everythingIncludeShorts = value }
        if let value = patch.theme { next.theme = value }
        return next
    }

    nonisolated static func message(for error: any Error) -> String {
        (error as? APIError)?.errorMessage ?? error.localizedDescription
    }
}
