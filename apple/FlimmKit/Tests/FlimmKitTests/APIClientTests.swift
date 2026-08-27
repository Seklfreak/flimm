import XCTest
@testable import FlimmKit

final class APIClientTests: XCTestCase {
    private let baseURL = URL(string: "https://flimm.example.com")!

    // MARK: - Base URL

    /// A URL pasted from a browser address bar has to work as well as a bare
    /// origin, because that is what people paste.
    func testBaseURLNormalisation() {
        XCTAssertEqual(
            APIClient.normalize(URL(string: "https://flimm.example.com/")!).absoluteString,
            "https://flimm.example.com"
        )
        XCTAssertEqual(
            APIClient.normalize(URL(string: "https://flimm.example.com/api/v1")!).absoluteString,
            "https://flimm.example.com"
        )
        XCTAssertEqual(
            ServerProbe.normalize("flimm.example.com")?.absoluteString,
            "https://flimm.example.com"
        )
        XCTAssertNil(ServerProbe.normalize(""))
        XCTAssertNil(ServerProbe.normalize("ftp://flimm.example.com"))
    }

    func testMediaURLResolution() {
        let client = APIClient(baseURL: baseURL)
        XCTAssertEqual(
            client.mediaURL("/media/video/yt-id.mp4")?.absoluteString,
            "https://flimm.example.com/media/video/yt-id.mp4"
        )
        // Already absolute: left alone.
        XCTAssertEqual(
            client.mediaURL("https://cdn.example.com/x.mp4")?.absoluteString,
            "https://cdn.example.com/x.mp4"
        )
    }

    /// `/media/*` takes a Bearer header for native clients; the cookie path is
    /// only there for browsers.
    func testMediaHeaders() async throws {
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok-1"))
        let headers = try await client.mediaHeaders()
        XCTAssertEqual(headers, ["Authorization": "Bearer tok-1"])

        let anonymous = APIClient(baseURL: baseURL, tokens: nil)
        let empty = try await anonymous.mediaHeaders()
        XCTAssertTrue(empty.isEmpty)
    }

    // MARK: - Path and query building

    func testNavCarriesTheWholeContext() async throws {
        let session = StubURLProtocol.session(json: Fixtures.nav)
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)

        let context = PlaybackContext.playlist("PL-1", shuffleSeed: "seed-1", audioOnly: true)
        _ = try await client.nav("yt-id", context: context)

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.method, "GET")
        XCTAssertEqual(request.path, "/api/v1/videos/yt-id/nav")
        // Same parameters, same order as the web client's links.
        XCTAssertEqual(request.query, "playlist=PL-1&shuffle=seed-1&audio=1")
        XCTAssertEqual(request.header("Authorization"), "Bearer tok")
    }

    func testUpNextAppendsPagingAfterTheContext() async throws {
        let session = StubURLProtocol.session(json: Fixtures.page)
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)

        _ = try await client.upNext("yt-id", context: .feed("feed-1"), page: 2, pageSize: 30)

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/api/v1/videos/yt-id/up-next")
        XCTAssertEqual(request.query, "feed=feed-1&page=2&page_size=30")
    }

    func testNavWithoutContextSendsNoQuery() async throws {
        let session = StubURLProtocol.session(json: Fixtures.navDetached)
        let client = APIClient(baseURL: baseURL, session: session)

        _ = try await client.nav("yt-id")

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertNil(request.query)
    }

    /// The heartbeat carries the playlist it is playing *from*, which is how
    /// the server knows to record nothing for a music playlist.
    func testProgressCarriesThePlaylistContext() async throws {
        let session = StubURLProtocol.session(json: Fixtures.progressResult)
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)

        _ = try await client.reportProgress("yt-id", position: 561.8, playlistId: "PL-music")

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.path, "/api/v1/videos/yt-id/progress")
        XCTAssertEqual(request.query, "playlist=PL-music")
        let body = try XCTUnwrap(request.body)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        // Seconds, floored — the contract's example is an integer.
        XCTAssertEqual(object["position"] as? Int, 561)
    }

    /// The prefetch/"is it ready yet?" call. It must not wait on the server,
    /// so all the client does is post and read the state back.
    func testStartHLSPostsAndReturnsTheState() async throws {
        let session = StubURLProtocol.session(json: Fixtures.hlsStatus)
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)

        let state = try await client.startHLS("yt-id")

        XCTAssertEqual(state, .running)
        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.path, "/api/v1/videos/yt-id/hls")
        XCTAssertNil(request.query)
        XCTAssertEqual(request.header("Authorization"), "Bearer tok")
    }

    /// The quality picker starts the rung it is about to play, and only that
    /// one: the server transcodes one job at a time.
    func testStartHLSCarriesTheChosenHeight() async throws {
        let session = StubURLProtocol.session(json: Fixtures.hlsStatus)
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)

        _ = try await client.startHLS("yt-id", height: 720)

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/api/v1/videos/yt-id/hls")
        XCTAssertEqual(request.query, "height=720")
    }

    func testFlagQueriesAreOmittedWhenFalse() async throws {
        let session = StubURLProtocol.session(json: Fixtures.searchResults)
        let client = APIClient(baseURL: baseURL, session: session)

        _ = try await client.search("shaping", scope: .subtitles, unseen: false, feed: nil)
        var request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.query, "q=shaping&scope=subtitles")

        _ = try await client.search("shaping", unseen: true, feed: "feed-1")
        request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.query, "q=shaping&unseen=true&feed=feed-1")
    }

    func testChannelListQuery() async throws {
        let session = StubURLProtocol.session(json: #"{"items":[],"page":0,"page_size":30,"total":0}"#)
        let client = APIClient(baseURL: baseURL, session: session)

        _ = try await client.channels(query: "chan", sort: .lastUpload, unfeeded: true, page: 1, pageSize: 50)

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.query, "q=chan&sort=last_upload&unfeeded=true&page=1&page_size=50")
    }

    func testDeleteReturns204WithNoBody() async throws {
        let session = StubURLProtocol.session { _, _ in (204, Data()) }
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)

        try await client.deleteHistoryEntry("entry-1")

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.method, "DELETE")
        XCTAssertEqual(request.path, "/api/v1/history/entry-1")
    }

    func testRequestBodiesUseSnakeCase() async throws {
        let session = StubURLProtocol.session { _, _ in (204, Data()) }
        let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)

        try await client.setChannelFeeds("UC-chan", feedIds: ["feed-1", "feed-2"])
        var body = try XCTUnwrap(StubURLProtocol.recorded.last?.body)
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(object["feed_ids"] as? [String], ["feed-1", "feed-2"])

        try await client.playlistAction("PL-1", videoId: "yt-id", action: .bottom)
        body = try XCTUnwrap(StubURLProtocol.recorded.last?.body)
        object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(object["video_id"] as? String, "yt-id")
        XCTAssertEqual(object["action"] as? String, "bottom")
    }

    /// `/config` is the one unauthenticated call, and must not wait on a token.
    func testConfigIsUnauthenticated() async throws {
        let session = StubURLProtocol.session(json: Fixtures.serverConfig)
        let client = APIClient(baseURL: baseURL, session: session)

        let config = try await client.config()
        XCTAssertEqual(config.oidcClientId, "flimm-native")

        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.path, "/api/v1/config")
        XCTAssertNil(request.header("Authorization"))
    }

    // MARK: - Errors

    func testErrorMapping() async throws {
        func statusError(_ status: Int, _ message: String = "boom") async -> APIError? {
            let session = StubURLProtocol.session { _, _ in (status, Data(#"{"error":"\#(message)"}"#.utf8)) }
            let client = APIClient(baseURL: baseURL, tokens: StaticTokenProvider("tok"), session: session)
            do {
                _ = try await client.feeds()
                return nil
            } catch let error as APIError {
                return error
            } catch {
                return nil
            }
        }

        // 404 for anything unknown *or* not yours: existence is never leaked.
        let notFound = await statusError(404)
        XCTAssertEqual(notFound, .notFound)

        let bad = await statusError(400, "bad feed")
        XCTAssertEqual(bad, .badRequest("bad feed"))

        let upstream = await statusError(502, "tubearchivist unavailable")
        XCTAssertEqual(upstream, .upstreamUnavailable("tubearchivist unavailable"))
        XCTAssertEqual(upstream?.isTransient, true)

        let teapot = await statusError(418, "no")
        XCTAssertEqual(teapot, .http(status: 418, message: "no"))
    }

    // MARK: - 401 → refresh → retry

    func testUnauthorizedTriggersOneRefreshAndOneRetry() async throws {
        let tokens = RecordingTokenProvider(initial: "stale", refreshed: "fresh")
        let session = StubURLProtocol.session { request, _ in
            let authorization = request.value(forHTTPHeaderField: "Authorization")
            if authorization == "Bearer fresh" {
                return (200, Data(Fixtures.me.utf8))
            }
            return (401, Data(#"{"error":"unauthorized"}"#.utf8))
        }
        let client = APIClient(baseURL: baseURL, tokens: tokens, session: session)

        _ = try await client.me()

        XCTAssertEqual(StubURLProtocol.recorded.count, 2)
        XCTAssertEqual(StubURLProtocol.recorded[0].header("Authorization"), "Bearer stale")
        XCTAssertEqual(StubURLProtocol.recorded[1].header("Authorization"), "Bearer fresh")
        let refreshes = await tokens.refreshCount
        XCTAssertEqual(refreshes, 1)
    }

    /// One retry, not a loop: a provider that keeps handing out dead tokens
    /// must not turn one screen into an infinite request storm.
    func testStillUnauthorizedAfterRefreshGivesUp() async throws {
        let tokens = RecordingTokenProvider(initial: "stale", refreshed: "also-stale")
        let session = StubURLProtocol.session { _, _ in (401, Data(#"{"error":"unauthorized"}"#.utf8)) }
        let client = APIClient(baseURL: baseURL, tokens: tokens, session: session)

        do {
            _ = try await client.me()
            XCTFail("expected unauthorized")
        } catch let error as APIError {
            XCTAssertEqual(error, .unauthorized)
        }
        XCTAssertEqual(StubURLProtocol.recorded.count, 2)
        let refreshes = await tokens.refreshCount
        XCTAssertEqual(refreshes, 1)
    }

    /// A refresh that fails because the network is down is transient — the
    /// caller must not read it as "signed out".
    func testTransientRefreshFailureIsNotUnauthorized() async throws {
        let tokens = RecordingTokenProvider(initial: "stale", refreshed: nil, refreshError: OIDCError.network("offline"))
        let session = StubURLProtocol.session { _, _ in (401, Data()) }
        let client = APIClient(baseURL: baseURL, tokens: tokens, session: session)

        do {
            _ = try await client.me()
            XCTFail("expected an error")
        } catch let error as APIError {
            XCTAssertTrue(error.isTransient, "got \(error)")
            XCTAssertNotEqual(error, .unauthorized)
        }
    }

    /// `invalid_grant` is the provider's definitive answer and *is* a sign-out.
    func testInvalidGrantDuringRefreshIsUnauthorized() async throws {
        let tokens = RecordingTokenProvider(initial: "stale", refreshed: nil, refreshError: OIDCError.invalidGrant)
        let session = StubURLProtocol.session { _, _ in (401, Data()) }
        let client = APIClient(baseURL: baseURL, tokens: tokens, session: session)

        do {
            _ = try await client.me()
            XCTFail("expected unauthorized")
        } catch let error as APIError {
            XCTAssertEqual(error, .unauthorized)
        }
    }

    func testTransportFailureIsTransient() async throws {
        let session = StubURLProtocol.session { _, _ in throw URLError(.notConnectedToInternet) }
        let client = APIClient(baseURL: baseURL, session: session)

        do {
            _ = try await client.config()
            XCTFail("expected an error")
        } catch let error as APIError {
            XCTAssertTrue(error.isTransient)
        }
    }
}

/// Counts refreshes so a test can prove there was exactly one.
actor RecordingTokenProvider: TokenProvider {
    private var token: String?
    private let refreshed: String?
    private let refreshError: (any Error)?
    private(set) var refreshCount = 0

    init(initial: String?, refreshed: String?, refreshError: (any Error)? = nil) {
        self.token = initial
        self.refreshed = refreshed
        self.refreshError = refreshError
    }

    func accessToken() async throws -> String? { token }

    func refreshAccessToken() async throws -> String? {
        refreshCount += 1
        if let refreshError { throw refreshError }
        token = refreshed
        return refreshed
    }
}

extension APIClientTests {
    /// AVFoundation has no public symbol for this key, so the exact spelling is
    /// load-bearing: a typo silently drops the bearer header and every media
    /// request 401s.
    func testAssetHTTPHeaderFieldsKeySpelling() {
        XCTAssertEqual(APIClient.assetHTTPHeaderFieldsKey, "AVURLAssetHTTPHeaderFieldsKey")
    }
}
