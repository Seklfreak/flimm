import Foundation

/// JSON cut from `docs/api.md`. Keeping the examples verbatim is the point:
/// when the contract changes, these stop decoding.
enum Fixtures {
    static let videoSummary = """
    {
      "id": "yt-id",
      "title": "Input shaping, finally explained",
      "channel": { "id": "UC-chan", "name": "A Channel", "thumb_url": "/media/thumb/channel/UC-chan" },
      "thumb_url": "/media/thumb/video/yt-id",
      "duration": 1476,
      "published": "2026-08-23T00:00:00Z",
      "downloaded": "2026-08-23T04:12:00Z",
      "type": "video",
      "subtitle_langs": ["en"],
      "has_auto_subtitles": true,
      "watched": false,
      "position": 561,
      "progress": 0.38,
      "last_played_at": "2026-08-26T15:42:00Z"
    }
    """

    /// A music-playlist item: no watch state is reported at all.
    static let videoSummaryWithoutWatchState = """
    {
      "id": "yt-song",
      "title": "A Song",
      "channel": { "id": "UC-band", "name": "A Band", "thumb_url": "/media/thumb/channel/UC-band" },
      "thumb_url": "/media/thumb/video/yt-song",
      "duration": 212,
      "published": "2026-01-02T03:04:05.123456Z",
      "downloaded": "2026-01-03T00:00:00Z",
      "type": "video",
      "subtitle_langs": [],
      "has_auto_subtitles": false,
      "last_played_at": null
    }
    """

    static let channelSummary = """
    {
      "id": "UC-chan", "name": "A Channel",
      "thumb_url": "/media/thumb/channel/UC-chan",
      "banner_url": "/media/thumb/channel/UC-chan/banner",
      "video_count": 212, "unseen_count": 3,
      "last_upload": "2026-08-25T00:00:00Z",
      "subscribed": true,
      "feeds": [ { "id": "feed-1", "name": "Home" } ]
    }
    """

    static let channelDetail = """
    {
      "id": "UC-chan", "name": "A Channel",
      "thumb_url": "/media/thumb/channel/UC-chan",
      "banner_url": "/media/thumb/channel/UC-chan/banner",
      "video_count": 212, "unseen_count": 3,
      "last_upload": "2026-08-25T00:00:00Z",
      "subscribed": true,
      "feeds": [],
      "description": "About this channel."
    }
    """

    static let videoDetail = """
    {
      "id": "yt-id",
      "title": "Input shaping, finally explained",
      "channel": \(channelSummary),
      "thumb_url": "/media/thumb/video/yt-id",
      "duration": 1476,
      "published": "2026-08-23T00:00:00Z",
      "downloaded": "2026-08-23T04:12:00Z",
      "type": "video",
      "subtitle_langs": ["en"],
      "has_auto_subtitles": true,
      "watched": false,
      "position": 561,
      "progress": 0.38,
      "last_played_at": "2026-08-26T15:42:00Z",
      "description": "A description.",
      "height": 1080,
      "media_url": "/media/video/yt-id.mp4",
      "audio_url": "/media/audio/yt-id.webm",
      "audio_aac_url": "/media/audio/yt-id.m4a",
      "hls_url": "/media/hls/yt-id/1080/index.m3u8",
      "hls_state": "done",
      "hls_variants": [ { "height": 1080, "url": "/media/hls/yt-id/1080/index.m3u8", "state": "done", "codec": "h264", "progress": 1 },
                        { "height": 720, "url": "/media/hls/yt-id/720/index.m3u8", "state": "pending", "codec": "h264" },
                        { "height": 480, "url": "/media/hls/yt-id/480/index.m3u8", "state": "pending", "codec": "h264" } ],
      "youtube_url": "https://www.youtube.com/watch?v=yt-id",
      "streams": [ { "type": "video", "codec": "avc1", "width": 1920, "height": 1080, "bitrate": 4500000 },
                   { "type": "audio", "codec": "mp4a", "width": 0, "height": 0, "bitrate": 130000 } ],
      "subtitles": [ { "lang": "en", "source": "user", "url": "/media/subtitles/yt-id/en.vtt" } ],
      "sponsorblock": [ { "category": "sponsor", "action_type": "skip", "start": 12.3, "end": 45.6 },
                        { "category": "selfpromo", "action_type": "mute", "start": 60, "end": 70 },
                        { "category": "poi_highlight", "action_type": "poi", "start": 100, "end": 100 },
                        { "category": "sponsor", "action_type": "something_new", "start": 200, "end": 210 } ],
      "stats": { "views": 0, "likes": 0 },
      "tags": [],
      "playlists": [ { "id": "PL-1", "name": "A Playlist", "position": 9, "count": 14 } ]
    }
    """

    /// The same detail from a backend that predates `streams`.
    static let videoDetailWithoutStreams: String = {
        let lines = videoDetail
            .split(separator: "\n", omittingEmptySubsequences: false)
            .filter { !$0.contains("\"streams\"") && !$0.contains("\"codec\": \"mp4a\"") }
        return lines.joined(separator: "\n")
    }()

    /// The same detail from a backend that predates `audio_aac_url` — native
    /// audio-only playback must fall back cleanly, not try `audio_url`.
    static let videoDetailWithoutAudioAac: String = {
        let lines = videoDetail
            .split(separator: "\n", omittingEmptySubsequences: false)
            .filter { !$0.contains("\"audio_aac_url\"") }
        return lines.joined(separator: "\n")
    }()

    /// The same detail from a backend that predates the compatible rendition.
    /// A video this device cannot decode is a dead end only here.
    static let videoDetailWithoutHLS: String = {
        let lines = videoDetail
            .split(separator: "\n", omittingEmptySubsequences: false)
            .filter { !$0.contains("\"hls_") && !$0.contains("/media/hls/") }
        return lines.joined(separator: "\n")
    }()

    /// A backend with `hls_url` but no ladder — the release between the two.
    /// A client that reads `hls_variants` still has to play these.
    static let videoDetailWithoutVariants: String = {
        let lines = videoDetail
            .split(separator: "\n", omittingEmptySubsequences: false)
            .filter { !$0.contains("\"hls_variants\"") && !$0.contains("\"height\": 720") && !$0.contains("\"height\": 480") }
        return lines.joined(separator: "\n")
    }()

    /// A 4K source: every rung, and HEVC above 1080p.
    static let videoDetail4K: String = {
        videoDetail.replacingOccurrences(
            of: "\"hls_variants\": [ {",
            with: """
            "hls_variants": [ { "height": 2160, "url": "/media/hls/yt-id/2160/index.m3u8", "state": "pending", "codec": "hevc" },
                              { "height": 1440, "url": "/media/hls/yt-id/1440/index.m3u8", "state": "pending", "codec": "hevc" },
                              {
            """
        )
    }()

    /// `POST /videos/{id}/hls`.
    static let hlsStatus = """
    { "state": "running", "progress": 0.37 }
    """

    static let feed = """
    {
      "id": "feed-1",
      "name": "Home",
      "channel_ids": ["UC-chan"],
      "channel_count": 6,
      "unseen_count": 7,
      "sort": "newest",
      "hide_seen": true,
      "include_shorts": false,
      "subtitles_only": false,
      "pinned": true,
      "position": 0,
      "created_at": "2026-08-01T00:00:00Z",
      "updated_at": "2026-08-20T12:00:00Z"
    }
    """

    static let everythingFeed = """
    {
      "id": "everything", "name": "Everything",
      "channel_ids": [], "channel_count": 12, "unseen_count": 41,
      "sort": "newest", "hide_seen": true, "include_shorts": false,
      "subtitles_only": false, "pinned": false, "position": 99,
      "created_at": "2026-08-01T00:00:00Z", "updated_at": "2026-08-01T00:00:00Z"
    }
    """

    static let playlistSummary = """
    {
      "id": "PL-1", "name": "A Playlist", "kind": "custom",
      "channel": null,
      "thumb_url": "/media/thumb/playlist/PL-1",
      "video_count": 14, "total_duration": 15120,
      "seen_count": 11, "in_progress_count": 1,
      "progress": 0.78,
      "resume_video_id": "yt-id",
      "pinned": false,
      "music": false
    }
    """

    static let musicPlaylistSummary = """
    {
      "id": "PL-music", "name": "Rotation", "kind": "custom",
      "channel": { "id": "UC-band", "name": "A Band" },
      "thumb_url": "/media/thumb/playlist/PL-music",
      "video_count": 40, "total_duration": 8600,
      "seen_count": 0, "in_progress_count": 0,
      "progress": 0,
      "resume_video_id": null,
      "pinned": true,
      "music": true
    }
    """

    static let playlistDetail = """
    {
      "id": "PL-1", "name": "A Playlist", "kind": "custom", "channel": null,
      "thumb_url": "/media/thumb/playlist/PL-1",
      "video_count": 1, "total_duration": 1476,
      "seen_count": 0, "in_progress_count": 1, "progress": 0.38,
      "resume_video_id": "yt-id", "pinned": true, "music": false,
      "items": [ { "position": 0, "video": \(videoSummary) } ]
    }
    """

    static let historyEntry = """
    {
      "id": "11111111-2222-3333-4444-555555555555",
      "video": \(videoSummary),
      "played_at": "2026-08-26T15:42:00Z",
      "state": "in_progress"
    }
    """

    static let page = """
    { "items": [ \(videoSummary) ], "page": 0, "page_size": 30, "total": 123 }
    """

    static let nav = """
    { "index": 8, "total": 14, "previous": \(videoSummary), "next": null, "first": \(videoSummary) }
    """

    static let navDetached = """
    { "index": -1, "total": 0, "previous": null, "next": null, "first": null }
    """

    static let chapters = """
    {
      "source": "embedded",
      "chapters": [ { "start": 0, "end": 132.5, "title": "Intro" },
                    { "start": 132.5, "end": 1476, "title": "The rest" } ]
    }
    """

    static let noChapters = """
    { "source": "none", "chapters": [] }
    """

    static let searchResults = """
    {
      "took_ms": 80,
      "videos":   { "total": 11, "items": [ { "video": \(videoSummary),
                    "subtitle_hits": [ { "lang": "en", "start": 400.2, "end": 404.9, "text": "…enable input shaping…" } ] } ] },
      "channels": { "total": 1, "items": [ { "id": "UC-chan", "name": "A Channel", "thumb_url": "", "banner_url": "",
                    "video_count": 212, "unseen_count": 3, "last_upload": null, "subscribed": true, "feeds": [],
                    "match_count": 4 } ] },
      "playlists": { "total": 2, "items": [ { "id": "PL-1", "name": "A Playlist", "kind": "custom", "channel": null,
                     "thumb_url": "", "video_count": 14, "total_duration": 15120, "seen_count": 11,
                     "in_progress_count": 1, "progress": 0.78, "resume_video_id": "yt-id", "pinned": false,
                     "music": false, "match_count": 2 } ] }
    }
    """

    static let me = """
    {
      "id": "user-1", "name": "A User", "email": "user@example.com", "is_admin": true,
      "prefs": {
        "autoplay": true,
        "playback_speed": 1.25,
        "subtitle_lang": "en",
        "subtitle_size": "large",
        "skip_sponsors": true,
        "everything_sort": "newest", "everything_hide_seen": true, "everything_include_shorts": false,
        "theme": "dark"
      }
    }
    """

    static let serverConfig = """
    {
      "app_name": "Flimm",
      "oidc_issuer": "https://auth.example.com/application/o/flimm/",
      "oidc_client_id": "flimm-native",
      "version": "1.4.2"
    }
    """

    static let serverConfigWithoutOIDC = """
    { "app_name": "Flimm", "oidc_issuer": "", "oidc_client_id": "", "version": "dev" }
    """

    static let progressResult = """
    { "position": 561, "watched": false }
    """
}
