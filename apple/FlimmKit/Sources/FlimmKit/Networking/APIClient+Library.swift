import Foundation

// Feeds, channels and playlists — everything that composes a library view.
extension APIClient {

    // MARK: - Feeds

    /// Every feed including the built-in `everything`, in `position` order.
    public func feeds() async throws -> [Feed] {
        try await get("/feeds")
    }

    public func feed(_ id: String) async throws -> Feed {
        try await get("/feeds/\(esc(id))")
    }

    public func createFeed(_ input: FeedInput) async throws -> Feed {
        try await send(.post, "/feeds", body: input)
    }

    /// Full update. `pinned: true` unpins every other feed.
    public func updateFeed(_ id: String, _ input: FeedInput) async throws -> Feed {
        try await send(.put, "/feeds/\(esc(id))", body: input)
    }

    /// Never touches channels or videos.
    public func deleteFeed(_ id: String) async throws {
        try await discard(.delete, "/feeds/\(esc(id))")
    }

    public func reorderFeeds(_ ids: [String]) async throws {
        try await discard(.post, "/feeds/reorder", body: IDList(ids: ids))
    }

    /// Omitting `view` lets the feed's own `hideSeen` choose.
    public func feedVideos(
        _ id: String,
        view: FeedView? = nil,
        page: Int = 0,
        cursor: String? = nil,
        pageSize: Int = Page<VideoSummary>.defaultSize
    ) async throws -> Page<VideoSummary> {
        var query = QueryBuilder()
        query.add("view", view)
        query.page(page, size: pageSize, cursor: cursor)
        return try await get("/feeds/\(esc(id))/videos", query: query.items)
    }

    /// Marks every currently unseen video in the feed watched.
    public func markFeedSeen(_ id: String) async throws {
        try await discard(.post, "/feeds/\(esc(id))/mark-seen")
    }

    // MARK: - Channels

    public func channels(
        query search: String? = nil,
        sort: ChannelSort? = nil,
        unfeeded: Bool = false,
        page: Int = 0,
        pageSize: Int = Page<ChannelSummary>.defaultSize
    ) async throws -> Page<ChannelSummary> {
        var query = QueryBuilder()
        query.add("q", search)
        query.add("sort", sort)
        query.flag("unfeeded", unfeeded)
        query.page(page, size: pageSize)
        return try await get("/channels", query: query.items)
    }

    public func channel(_ id: String) async throws -> Channel {
        try await get("/channels/\(esc(id))")
    }

    public func channelVideos(
        _ id: String,
        view: ChannelView = .all,
        sort: FeedSort? = nil,
        page: Int = 0,
        cursor: String? = nil,
        pageSize: Int = Page<VideoSummary>.defaultSize
    ) async throws -> Page<VideoSummary> {
        var query = QueryBuilder()
        query.add("view", view)
        query.add("sort", sort)
        query.page(page, size: pageSize, cursor: cursor)
        return try await get("/channels/\(esc(id))/videos", query: query.items)
    }

    public func channelPlaylists(_ id: String) async throws -> [PlaylistSummary] {
        try await get("/channels/\(esc(id))/playlists")
    }

    /// The "In feeds:" control — replaces the channel's feed membership.
    public func setChannelFeeds(_ id: String, feedIds: [String]) async throws {
        try await discard(.put, "/channels/\(esc(id))/feeds", body: FeedIDList(feedIds: feedIds))
    }

    public func markChannelSeen(_ id: String) async throws {
        try await discard(.post, "/channels/\(esc(id))/mark-seen")
    }

    // MARK: - Playlists

    /// Unpaged, in pin order. Only playlists that still resolve in
    /// TubeArchivist come back, so a stale pin can never wedge the sidebar.
    public func pinnedPlaylists() async throws -> [PlaylistSummary] {
        try await get("/playlists/pinned")
    }

    /// Pinning appends to the end; unpinning closes the gap.
    public func setPlaylistPinned(_ id: String, pinned: Bool) async throws {
        try await discard(.put, "/playlists/\(esc(id))/pinned", body: PinnedBody(pinned: pinned))
    }

    /// Marks the playlist as music: audio-only playback, and no watch state
    /// recorded or reported.
    public func setPlaylistMusic(_ id: String, music: Bool) async throws {
        try await discard(.put, "/playlists/\(esc(id))/music", body: MusicBody(music: music))
    }

    /// Custom playlists come first.
    public func playlists(
        kind: PlaylistKind? = nil,
        page: Int = 0,
        pageSize: Int = Page<PlaylistSummary>.defaultSize
    ) async throws -> Page<PlaylistSummary> {
        var query = QueryBuilder()
        query.add("kind", kind)
        query.page(page, size: pageSize)
        return try await get("/playlists", query: query.items)
    }

    public func playlist(_ id: String) async throws -> Playlist {
        try await get("/playlists/\(esc(id))")
    }

    public func createPlaylist(name: String) async throws -> PlaylistSummary {
        try await send(.post, "/playlists", body: NameBody(name: name))
    }

    /// Custom playlists only.
    public func renamePlaylist(_ id: String, name: String) async throws -> PlaylistSummary {
        try await send(.patch, "/playlists/\(esc(id))", body: NameBody(name: name))
    }

    /// Custom playlists only; the videos themselves are untouched.
    public func deletePlaylist(_ id: String) async throws {
        try await discard(.delete, "/playlists/\(esc(id))")
    }

    public func playlistAction(_ id: String, videoId: String, action: PlaylistAction) async throws {
        try await discard(.post, "/playlists/\(esc(id))/videos", body: PlaylistActionBody(videoId: videoId, action: action))
    }
}

/// `sort=` on `GET /channels`.
public enum ChannelSort: String, Codable, Sendable, CaseIterable {
    case name
    case videos
    case unseen
    case lastUpload = "last_upload"
}

/// `view=` on `GET /channels/{id}/videos`.
public enum ChannelView: String, Codable, Sendable, CaseIterable {
    case all
    case unseen
}

// Request bodies small enough not to deserve their own file.
struct IDList: Encodable { let ids: [String] }
struct FeedIDList: Encodable { let feedIds: [String] }
struct PinnedBody: Encodable { let pinned: Bool }
struct MusicBody: Encodable { let music: Bool }
struct NameBody: Encodable { let name: String }
struct WatchedBody: Encodable { let watched: Bool }
struct PositionBody: Encodable { let position: Int }
struct PlaylistActionBody: Encodable {
    let videoId: String
    let action: PlaylistAction
}

/// Percent-escapes an id for use in a path segment. TubeArchivist ids are
/// URL-safe, but a user-typed feed id never should be trusted to be.
func esc(_ value: String) -> String {
    value.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? value
}
