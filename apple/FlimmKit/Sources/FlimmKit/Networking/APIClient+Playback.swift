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

    /// How loud a video is, and the gain to play it at. Asking is what starts
    /// the measurement server-side; see ``LoudnessGain``.
    public func loudness(_ id: String) async throws -> LoudnessInfo {
        try await get("/videos/\(esc(id))/loudness")
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

    /// Takes the video out of every feed and out of *up next* without
    /// watching it. This is Flimm's own state — never written to
    /// TubeArchivist — so it says nothing about `watched`. Verified against
    /// TA first, so an unknown id is a 404; idempotent, and the original
    /// dismissal time is kept on a repeat call.
    @discardableResult
    public func dismiss(_ id: String) async throws -> Bool {
        let result: DismissResult = try await send(.post, "/videos/\(esc(id))/dismiss")
        return result.dismissed
    }

    /// Puts a dismissed video back. Undoing a non-dismissal is still a
    /// success, so an undo control can never fail on a double tap.
    @discardableResult
    public func undismiss(_ id: String) async throws -> Bool {
        let result: DismissResult = try await send(.delete, "/videos/\(esc(id))/dismiss")
        return result.dismissed
    }

    /// Starts — or steers — a compatible rendition **without waiting** for it,
    /// and reports where it stands.
    ///
    /// `height` picks the rung of `hls_variants`; without one the server
    /// starts the height `hls_url` points at. A height the video does not
    /// offer is a 400, so pass one that came from the video's own ladder.
    ///
    /// `from` is where playback is about to start, in seconds: the server
    /// encodes that part of the video first, so resuming an hour in does not
    /// wait for the first 40 minutes nobody is going to watch. Pass the resume
    /// position the server itself handed over (`position`), 0 for "start
    /// over", and the target of a seek that landed outside what has been
    /// produced — the encoder is re-pointed rather than restarted. It is sent
    /// as whole seconds.
    ///
    /// Idempotent — a running or finished rendition is not started again — so
    /// it doubles as "how far along is it?", which is what a player waiting on
    /// a segment that does not exist yet wants to know: ``HLSStatus/progress``
    /// is what "Preparing… 37%" is made of. Call it before opening the asset
    /// so the transcode is already running while AVFoundation connects, and
    /// call it ahead of time to prefetch the next video in a queue. The server
    /// runs one transcode at a time, so never warm several heights of the same
    /// video: ask for the one that will be played.
    @discardableResult
    public func startHLS(_ id: String, height: Int? = nil, from: Double? = nil) async throws -> HLSStatus {
        var query = QueryBuilder()
        query.add("height", height)
        query.add("from", from.map { Int(max(0, $0).rounded(.down)) })
        return try await send(.post, "/videos/\(esc(id))/hls", query: query.items)
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
