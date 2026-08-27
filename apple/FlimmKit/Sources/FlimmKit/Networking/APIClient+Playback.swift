import Foundation

// Video detail, the context-aware navigation around it, watch state, history
// and search.
extension APIClient {

    // MARK: - Videos

    public func video(_ id: String) async throws -> Video {
        try await get("/videos/\(esc(id))")
    }

    /// Everything following the video in the playback context, falling back to
    /// `similar` when nothing does. Paged so a long playlist scrolls rather
    /// than being truncated.
    public func upNext(
        _ id: String,
        context: PlaybackContext = .none,
        page: Int = 0,
        pageSize: Int = Page<VideoSummary>.defaultSize
    ) async throws -> Page<VideoSummary> {
        var query = QueryBuilder(context.queryItems)
        query.page(page, size: pageSize)
        return try await get("/videos/\(esc(id))/up-next", query: query.items)
    }

    /// The same list as ``upNext(_:context:page:pageSize:)``, addressed by
    /// position rather than sliced — the source of previous/next and of
    /// `first`, the entry point for a shuffled run.
    public func nav(_ id: String, context: PlaybackContext = .none) async throws -> Nav {
        try await get("/videos/\(esc(id))/nav", query: context.queryItems)
    }

    public func similar(_ id: String) async throws -> [VideoSummary] {
        try await get("/videos/\(esc(id))/similar")
    }

    /// TubeArchivist passthrough; the shape is not pinned down by the contract.
    public func comments(_ id: String) async throws -> [VideoComment] {
        try await get("/videos/\(esc(id))/comments")
    }

    /// Scrubber markers and the chapter list. An empty list means "no chapter
    /// UI", never an error.
    public func chapters(_ id: String) async throws -> Chapters {
        try await get("/videos/\(esc(id))/chapters")
    }

    /// The playback heartbeat.
    ///
    /// `playlistId` is the *context* being played from, not the video's
    /// playlist membership: when it names a music playlist the server records
    /// nothing at all. Every heartbeat path must pass it.
    ///
    /// The server owns both thresholds — what counts as watched (≥90%, or ≤30s
    /// remaining) and what is too brief to record (`MIN_PLAY_SECONDS`). Do not
    /// reimplement either.
    @discardableResult
    public func reportProgress(_ id: String, position: Double, playlistId: String? = nil) async throws -> ProgressResult {
        var query = QueryBuilder()
        query.add("playlist", playlistId)
        return try await send(
            .post,
            "/videos/\(esc(id))/progress",
            query: query.items,
            body: PositionBody(position: Int(position.rounded(.down)))
        )
    }

    /// `false` clears the position and TubeArchivist's progress too.
    public func setWatched(_ id: String, watched: Bool) async throws {
        try await discard(.post, "/videos/\(esc(id))/watched", body: WatchedBody(watched: watched))
    }

    /// Starts the compatible H.264/AAC rendition (`hls_url`) **without
    /// waiting** for it, and reports where it stands.
    ///
    /// Idempotent — a running or finished rendition is not started again — so
    /// it doubles as "how far along is it?", which is what a player retrying a
    /// not-yet-ready playlist wants to know. Call it before opening the asset
    /// so the transcode is already running while AVFoundation connects, and
    /// call it ahead of time to prefetch the next video in a queue.
    @discardableResult
    public func startHLS(_ id: String) async throws -> HLSState {
        let status: HLSStatus = try await send(.post, "/videos/\(esc(id))/hls")
        return status.state
    }

    /// "Start over": position → 0 and TubeArchivist progress deleted.
    public func startOver(_ id: String) async throws {
        try await discard(.delete, "/videos/\(esc(id))/progress")
    }

    // MARK: - History

    /// Newest first. Entries below `MIN_PLAY_SECONDS` that never completed are
    /// excluded server-side.
    public func history(
        filter: HistoryFilter = .all,
        query search: String? = nil,
        page: Int = 0,
        pageSize: Int = Page<HistoryEntry>.defaultSize
    ) async throws -> Page<HistoryEntry> {
        var query = QueryBuilder()
        query.add("filter", filter)
        query.add("q", search)
        query.page(page, size: pageSize)
        return try await get("/history", query: query.items)
    }

    /// Hides the entry. Watch state is unchanged, so the video stays seen.
    public func deleteHistoryEntry(_ entryId: String) async throws {
        try await discard(.delete, "/history/\(esc(entryId))")
    }

    // MARK: - Search

    public func search(
        _ text: String,
        scope: SearchScope? = nil,
        unseen: Bool = false,
        feed: String? = nil
    ) async throws -> SearchResults {
        var query = QueryBuilder()
        query.add("q", text)
        query.add("scope", scope)
        query.flag("unseen", unseen)
        query.add("feed", feed)
        return try await get("/search", query: query.items)
    }
}
