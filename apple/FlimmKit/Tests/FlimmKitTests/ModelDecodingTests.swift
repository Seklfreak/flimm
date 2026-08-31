import XCTest
@testable import FlimmKit

/// Every object in `docs/api.md`, decoded from the contract's own examples.
final class ModelDecodingTests: XCTestCase {
    private let decoder = FlimmCoding.decoder

    private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try decoder.decode(type, from: Data(json.utf8))
    }

    /// "Unknown" and "none" are different answers, and only one of them draws
    /// a control. A server without Return YouTube Dislike omits the key.
    func testVoteCountsSayWhenNobodyKnowsTheDislikes() throws {
        let known = try decode(VideoStats.self, #"{"views": 900, "likes": 45120, "dislikes": 1183}"#)
        XCTAssertEqual(known.likes, 45120)
        XCTAssertEqual(known.dislikes, 1183)

        let unknown = try decode(VideoStats.self, #"{"views": 900, "likes": 40}"#)
        XCTAssertNil(unknown.dislikes)

        // A video the service knows and that genuinely has none.
        let none = try decode(VideoStats.self, #"{"views": 900, "likes": 40, "dislikes": 0}"#)
        XCTAssertEqual(none.dislikes, 0)
    }

    /// A client draws 24 columns and 7 days; a short array from an older
    /// server must not make it decide what a missing hour means.
    func testWatchStatsPadsTheBreakdowns() throws {
        let stats = try decode(WatchStats.self, #"""
        {"started": 412, "finished": 297, "seconds": 987654, "since": "2026-01-02T15:04:00Z",
         "range": "year", "zone": "America/New_York",
         "top_channels": [{"id": "UC1", "name": "Slow Kitchen", "videos": 40, "seconds": 120000}],
         "by_hour": [1, 2, 3], "by_weekday": [], "by_month": [{"month": "2026-07", "videos": 30, "seconds": 60000}]}
        """#)
        XCTAssertEqual(stats.byHour.count, 24)
        XCTAssertEqual(stats.byHour[0], 1)
        XCTAssertEqual(stats.byHour[23], 0)
        XCTAssertEqual(stats.byWeekday, Array(repeating: 0, count: 7))
        XCTAssertEqual(stats.range, .year)
        XCTAssertEqual(stats.topChannels.first?.name, "Slow Kitchen")
        XCTAssertEqual(stats.byMonth.first?.videos, 30)
        XCTAssertEqual(stats.finishRate ?? 0, 297.0 / 412.0, accuracy: 0.0001)
    }

    /// A rate over no videos is nothing, not 0% — the difference is a screen
    /// that says "nothing watched yet" instead of one that says you finish 0%.
    func testFinishRateIsNilWithNoHistory() {
        XCTAssertNil(WatchStats().finishRate)
    }

    func testVideoSummary() throws {
        let video = try decode(VideoSummary.self, Fixtures.videoSummary)
        XCTAssertEqual(video.id, "yt-id")
        XCTAssertEqual(video.channel.id, "UC-chan")
        XCTAssertEqual(video.thumbUrl, "/media/thumb/video/yt-id")
        XCTAssertEqual(video.duration, 1476)
        XCTAssertEqual(video.type, .video)
        XCTAssertEqual(video.subtitleLangs, ["en"])
        XCTAssertTrue(video.hasAutoSubtitles)
        XCTAssertEqual(video.position, 561)
        XCTAssertEqual(video.progress, 0.38, accuracy: 0.0001)
        XCTAssertNotNil(video.lastPlayedAt)
        // Any position on an unwatched video means "in progress".
        XCTAssertTrue(video.isInProgress)
        XCTAssertFalse(video.dismissed)
    }

    /// A music playlist reports no watch state at all; the fields must default
    /// rather than fail the response. `dismissed` falls back the same way.
    func testVideoSummaryWithoutWatchState() throws {
        let video = try decode(VideoSummary.self, Fixtures.videoSummaryWithoutWatchState)
        XCTAssertFalse(video.watched)
        XCTAssertEqual(video.position, 0)
        XCTAssertEqual(video.progress, 0)
        XCTAssertNil(video.lastPlayedAt)
        XCTAssertFalse(video.isInProgress)
        XCTAssertFalse(video.dismissed)
    }

    /// Channels, playlists, search and history still show a dismissed video —
    /// that is where a viewer finds one again and puts it back.
    func testVideoSummaryDismissed() throws {
        let json = Fixtures.videoSummary.replacingOccurrences(
            of: "\"dismissed\": false", with: "\"dismissed\": true"
        )
        let video = try decode(VideoSummary.self, json)
        XCTAssertTrue(video.dismissed)
    }

    /// Go marshals `time.Time` as RFC 3339 Nano: whole seconds lose the
    /// fraction, everything else keeps it. Both must parse.
    func testRFC3339BothShapes() throws {
        let plain = try decode(VideoSummary.self, Fixtures.videoSummary)
        XCTAssertEqual(
            plain.published,
            Date(timeIntervalSince1970: 1_787_443_200) // 2026-08-23T00:00:00Z
        )

        let fractional = try decode(VideoSummary.self, Fixtures.videoSummaryWithoutWatchState)
        let published = try XCTUnwrap(fractional.published)
        XCTAssertEqual(published.timeIntervalSince1970, 1_767_323_045.123, accuracy: 0.01)
    }

    func testVideoDetail() throws {
        let video = try decode(Video.self, Fixtures.videoDetail)
        XCTAssertEqual(video.mediaUrl, "/media/video/yt-id.mp4")
        XCTAssertEqual(video.audioUrl, "/media/audio/yt-id.webm")
        XCTAssertEqual(video.audioAacURL, "/media/audio/yt-id.m4a")
        XCTAssertEqual(video.nativeAudioURL, "/media/audio/yt-id.m4a")
        XCTAssertEqual(video.hlsURL, "/media/hls/yt-id/1080/index.m3u8")
        XCTAssertEqual(video.compatibleVideoURL, "/media/hls/yt-id/1080/index.m3u8")
        XCTAssertEqual(video.hlsState, .done)
        // The ladder, tallest first, each rung with its own state.
        XCTAssertEqual(video.hlsLadder.map(\.height), [1080, 720, 480])
        XCTAssertEqual(video.hlsLadder.map(\.state), [.done, .pending, .pending])
        XCTAssertEqual(video.hlsLadder.map(\.codec), [.h264, .h264, .h264])
        XCTAssertEqual(video.hlsLadder.first?.url, "/media/hls/yt-id/1080/index.m3u8")
        XCTAssertEqual(video.height, 1080)
        XCTAssertEqual(video.channel.videoCount, 212)
        XCTAssertEqual(video.subtitles.first?.source, .user)
        XCTAssertEqual(video.sponsorblock.first?.category, "sponsor")
        XCTAssertEqual(video.sponsorblock.map(\.actionType), [.skip, .mute, .poi, .other])
        XCTAssertEqual(video.playlists.first?.position, 9)
        XCTAssertFalse(video.dismissed)
        XCTAssertEqual(video.summary.id, video.id)
        // The stub form carries dismissed through, like every other watch field.
        XCTAssertEqual(video.summary.dismissed, video.dismissed)

        let streams = try XCTUnwrap(video.streams)
        XCTAssertEqual(streams.count, 2)
        XCTAssertEqual(streams[0].type, .video)
        XCTAssertEqual(streams[0].codec, "avc1")
        XCTAssertEqual(streams[0].width, 1920)
        XCTAssertEqual(streams[1].bitrate, 130_000)
        // avc1 + mp4a is the one combination AVFoundation always decodes.
        XCTAssertTrue(streams.allSatisfy(\.isNativelyPlayable))
    }

    /// `streams` arrives in a later backend release, so a server without it
    /// must still decode.
    func testVideoDetailWithoutStreams() throws {
        let video = try decode(Video.self, Fixtures.videoDetailWithoutStreams)
        XCTAssertNil(video.streams)
    }

    /// `audio_aac_url` arrives in a later backend release too; a server
    /// without it must still decode, and `nativeAudioURL` must report `nil`
    /// rather than falling back to the unplayable `audio_url`.
    func testVideoDetailWithoutAudioAac() throws {
        let video = try decode(Video.self, Fixtures.videoDetailWithoutAudioAac)
        XCTAssertNil(video.audioAacURL)
        XCTAssertNil(video.nativeAudioURL)
        XCTAssertEqual(video.audioUrl, "/media/audio/yt-id.webm")
    }

    /// `hls_url` arrives in a later backend release as well; a server without
    /// it must still decode, and report no compatible rendition rather than
    /// an empty path.
    func testVideoDetailWithoutHLS() throws {
        let video = try decode(Video.self, Fixtures.videoDetailWithoutHLS)
        XCTAssertNil(video.hlsURL)
        XCTAssertNil(video.hlsState)
        XCTAssertNil(video.compatibleVideoURL)
    }

    /// A state the contract grows later must not fail the whole video detail.
    func testUnknownHLSStateDecodes() throws {
        let source = Fixtures.videoDetail.replacingOccurrences(of: "\"hls_state\": \"done\"", with: "\"hls_state\": \"queued\"")
        let video = try decode(Video.self, source)
        XCTAssertEqual(video.hlsState, .unknown)
        XCTAssertFalse(try XCTUnwrap(video.hlsState).isPreparing)
    }

    /// A backend between the two releases: one rendition, no ladder. The
    /// ladder must be empty rather than invented.
    func testVideoDetailWithoutVariants() throws {
        let video = try decode(Video.self, Fixtures.videoDetailWithoutVariants)
        XCTAssertNil(video.hlsVariants)
        XCTAssertTrue(video.hlsLadder.isEmpty)
        XCTAssertEqual(video.compatibleVideoURL, "/media/hls/yt-id/1080/index.m3u8")
    }

    /// HEVC above 1080p, and a codec added to the contract later decodes as
    /// `unknown` rather than failing the whole video detail.
    func testHLSVariantCodecs() throws {
        let video = try decode(Video.self, Fixtures.videoDetail4K)
        XCTAssertEqual(video.hlsLadder.map(\.height), [2160, 1440, 1080, 720, 480])
        XCTAssertEqual(video.hlsLadder.map(\.codec), [.hevc, .hevc, .h264, .h264, .h264])

        let future = Fixtures.videoDetail4K.replacingOccurrences(of: "\"codec\": \"hevc\"", with: "\"codec\": \"av1\"")
        XCTAssertEqual(try decode(Video.self, future).hlsLadder.first?.codec, .unknown)
    }

    /// Each rung says how far its own encode has got, and a rung from a
    /// backend that predates the field reads as 0 rather than failing.
    func testHLSVariantProgress() throws {
        let video = try decode(Video.self, Fixtures.videoDetail)
        XCTAssertEqual(video.hlsLadder.map(\.progress), [1, 0, 0])

        let running = Fixtures.videoDetail.replacingOccurrences(
            of: "\"state\": \"done\", \"codec\": \"h264\", \"progress\": 1",
            with: "\"state\": \"running\", \"codec\": \"h264\", \"progress\": 0.37"
        )
        let rung = try XCTUnwrap(try decode(Video.self, running).hlsLadder.first)
        XCTAssertEqual(rung.state, .running)
        XCTAssertEqual(rung.progress, 0.37, accuracy: 0.0001)

        // The backend spells it `hls_progress`; a bare `progress` is read too,
        // so a rename on either side cannot silently decode as 0.
        let prefixed = Fixtures.videoDetail.replacingOccurrences(of: "\"progress\": 1", with: "\"hls_progress\": 1")
        XCTAssertEqual(try decode(Video.self, prefixed).hlsLadder.map(\.progress), [1, 0, 0])
    }

    /// A rung with no URL is not something to hand to `AVPlayer`.
    func testEmptyVariantsAreDroppedFromTheLadder() throws {
        let source = Fixtures.videoDetail.replacingOccurrences(of: "\"/media/hls/yt-id/720/index.m3u8\"", with: "\"\"")
        XCTAssertEqual(try decode(Video.self, source).hlsLadder.map(\.height), [1080, 480])
    }

    func testHLSStatePreparing() {
        XCTAssertTrue(HLSState.pending.isPreparing)
        XCTAssertTrue(HLSState.running.isPreparing)
        XCTAssertFalse(HLSState.done.isPreparing)
        XCTAssertFalse(HLSState.failed.isPreparing)
    }

    func testMediaStreamPlayability() {
        XCTAssertFalse(MediaStream(type: .video, codec: "vp09.00.40.08").isNativelyPlayable)
        XCTAssertFalse(MediaStream(type: .video, codec: "av01.0.05M.08").isNativelyPlayable)
        XCTAssertFalse(MediaStream(type: .audio, codec: "opus").isNativelyPlayable)
        XCTAssertTrue(MediaStream(type: .video, codec: "avc1.640028").isNativelyPlayable)
    }

    func testChannelSummaryAndDetail() throws {
        let summary = try decode(ChannelSummary.self, Fixtures.channelSummary)
        XCTAssertTrue(summary.pinned)
        XCTAssertEqual(summary.unseenCount, 3)
        XCTAssertEqual(summary.feeds.first, FeedRef(id: "feed-1", name: "Home"))

        let detail = try decode(Channel.self, Fixtures.channelDetail)
        XCTAssertEqual(detail.id, "UC-chan")
        XCTAssertEqual(detail.description, "About this channel.")
        XCTAssertEqual(detail.summary.videoCount, 212)
    }

    func testFeed() throws {
        let feed = try decode(Feed.self, Fixtures.feed)
        XCTAssertEqual(feed.channelIds, ["UC-chan"])
        XCTAssertEqual(feed.sort, .newest)
        XCTAssertTrue(feed.pinned)
        XCTAssertFalse(feed.isEverything)
        XCTAssertNotNil(feed.createdAt)

        XCTAssertEqual(feed.playlistIds, ["PL-9"])
        XCTAssertEqual(feed.playlistCount, 1)
        XCTAssertEqual(feed.seriesWatchChannelIds, ["UC-watch"])

        let everything = try decode(Feed.self, Fixtures.everythingFeed)
        XCTAssertTrue(everything.isEverything)
        XCTAssertTrue(everything.channelIds.isEmpty)
        // A server from before playlist sources existed sends no playlist_ids;
        // the field must default rather than fail the decode.
        XCTAssertTrue(everything.playlistIds.isEmpty)
    }

    func testFeedInputRoundTrip() throws {
        let feed = try decode(Feed.self, Fixtures.feed)
        let input = FeedInput(feed: feed)
        let encoded = try FlimmCoding.encoder.encode(input)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: encoded) as? [String: Any])
        XCTAssertEqual(object["name"] as? String, "Home")
        XCTAssertEqual(object["channel_ids"] as? [String], ["UC-chan"])
        XCTAssertEqual(object["playlist_ids"] as? [String], ["PL-9"])
        XCTAssertEqual(object["series_watch_channel_ids"] as? [String], ["UC-watch"])
        XCTAssertEqual(object["hide_seen"] as? Bool, true)
        XCTAssertEqual(object["subtitles_only"] as? Bool, false)
        XCTAssertEqual(object["pinned"] as? Bool, true)
    }

    func testPlaylistSummaryAndDetail() throws {
        let summary = try decode(PlaylistSummary.self, Fixtures.playlistSummary)
        XCTAssertEqual(summary.kind, .custom)
        XCTAssertNil(summary.channel)
        XCTAssertEqual(summary.resumeVideoId, "yt-id")
        XCTAssertFalse(summary.pinned)
        XCTAssertFalse(summary.music)
        XCTAssertEqual(summary.feeds.first?.name, "Home")

        let music = try decode(PlaylistSummary.self, Fixtures.musicPlaylistSummary)
        XCTAssertTrue(music.music)
        XCTAssertTrue(music.pinned)
        XCTAssertEqual(music.channel?.name, "A Band")
        // Watch state comes back zeroed for a music playlist.
        XCTAssertEqual(music.seenCount, 0)
        XCTAssertNil(music.resumeVideoId)

        let detail = try decode(Playlist.self, Fixtures.playlistDetail)
        XCTAssertEqual(detail.items.count, 1)
        XCTAssertEqual(detail.items.first?.position, 0)
        XCTAssertEqual(detail.items.first?.video.id, "yt-id")
        XCTAssertEqual(detail.summary.videoCount, 1)
    }

    func testHistoryEntry() throws {
        let entry = try decode(HistoryEntry.self, Fixtures.historyEntry)
        XCTAssertEqual(entry.id, "11111111-2222-3333-4444-555555555555")
        XCTAssertEqual(entry.state, .inProgress)
        XCTAssertEqual(entry.video.id, "yt-id")
        XCTAssertNotNil(entry.playedAt)
    }

    func testPage() throws {
        let page = try decode(Page<VideoSummary>.self, Fixtures.page)
        XCTAssertEqual(page.items.count, 1)
        XCTAssertEqual(page.page, 0)
        XCTAssertEqual(page.pageSize, 30)
        XCTAssertEqual(page.total, 123)
        XCTAssertTrue(page.hasMore)
    }

    func testNav() throws {
        let nav = try decode(Nav.self, Fixtures.nav)
        XCTAssertEqual(nav.index, 8)
        XCTAssertEqual(nav.total, 14)
        XCTAssertNotNil(nav.previous)
        XCTAssertNil(nav.next)
        XCTAssertEqual(nav.first?.id, "yt-id")
        XCTAssertFalse(nav.isDetached)

        // Opened without a context, or dropped out of a hide-seen feed.
        let detached = try decode(Nav.self, Fixtures.navDetached)
        XCTAssertTrue(detached.isDetached)
    }

    func testChapters() throws {
        let chapters = try decode(Chapters.self, Fixtures.chapters)
        XCTAssertEqual(chapters.source, .embedded)
        XCTAssertEqual(chapters.chapters.count, 2)
        XCTAssertEqual(chapters.chapters[0].end, 132.5)
        XCTAssertFalse(chapters.isEmpty)

        // An empty list is "no chapter UI", never an error.
        let empty = try decode(Chapters.self, Fixtures.noChapters)
        XCTAssertEqual(empty.source, ChaptersSource.none)
        XCTAssertTrue(empty.isEmpty)
    }

    func testSearchResults() throws {
        let results = try decode(SearchResults.self, Fixtures.searchResults)
        XCTAssertEqual(results.tookMs, 80)
        XCTAssertEqual(results.videos.total, 11)
        XCTAssertEqual(results.videos.items.first?.video.id, "yt-id")
        XCTAssertEqual(results.videos.items.first?.subtitleHits.first?.start, 400.2)
        XCTAssertEqual(results.channels.items.first?.matchCount, 4)
        XCTAssertEqual(results.channels.items.first?.channel.name, "A Channel")
        XCTAssertEqual(results.playlists.items.first?.matchCount, 2)
        XCTAssertEqual(results.playlists.items.first?.playlist.id, "PL-1")
        XCTAssertFalse(results.isEmpty)
    }

    func testMeAndPrefs() throws {
        let me = try decode(Me.self, Fixtures.me)
        XCTAssertTrue(me.isAdmin)
        XCTAssertEqual(me.prefs.playbackSpeed, 1.25)
        XCTAssertEqual(me.prefs.subtitleSize, .large)
        XCTAssertEqual(me.prefs.theme, .dark)
        XCTAssertEqual(me.prefs.everythingSort, .newest)
    }

    func testPrefsPatchOmitsUnsetFields() throws {
        let patch = PrefsPatch(subtitleLang: Prefs.subtitlesOff, theme: .light)
        let encoded = try FlimmCoding.encoder.encode(patch)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: encoded) as? [String: Any])
        XCTAssertEqual(object.keys.sorted(), ["subtitle_lang", "theme"])
        XCTAssertEqual(object["subtitle_lang"] as? String, "off")
    }

    func testServerConfig() throws {
        let config = try decode(ServerConfig.self, Fixtures.serverConfig)
        XCTAssertEqual(config.appName, "Flimm")
        XCTAssertEqual(config.oidcClientId, "flimm-native")
        XCTAssertTrue(config.hasOIDC)
        XCTAssertNotNil(config.issuerURL)

        // AUTH_DISABLED=true deployments publish no issuer.
        let bare = try decode(ServerConfig.self, Fixtures.serverConfigWithoutOIDC)
        XCTAssertFalse(bare.hasOIDC)
    }

    func testProgressResult() throws {
        let result = try decode(ProgressResult.self, Fixtures.progressResult)
        XCTAssertEqual(result.position, 561)
        XCTAssertFalse(result.watched)
    }
}
