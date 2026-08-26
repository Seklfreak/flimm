import Foundation

public enum VideoKind: String, Codable, Sendable, CaseIterable {
    case video
    case short
    case stream
}

/// The channel stub embedded in a `VideoSummary` — id, name and thumbnail
/// only. The full object is ``ChannelSummary``.
public struct VideoChannelRef: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let thumbUrl: String

    public init(id: String, name: String, thumbUrl: String) {
        self.id = id
        self.name = name
        self.thumbUrl = thumbUrl
    }
}

/// A video as it appears in any list.
///
/// `watched`, `position`, `progress` and `lastPlayedAt` are per user. A
/// playlist marked `music` reports none of them, so they all decode with a
/// zero fallback rather than being optional at every call site.
public struct VideoSummary: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let title: String
    public let channel: VideoChannelRef
    public let thumbUrl: String
    public let duration: Double
    public let published: Date?
    public let downloaded: Date?
    public let type: VideoKind
    /// Archived subtitle tracks; empty when the video has none.
    public let subtitleLangs: [String]
    public let hasAutoSubtitles: Bool
    public let watched: Bool
    /// Resume position in seconds, 0 when there is none.
    public let position: Double
    /// `position / duration`; 0 or 1 once watched.
    public let progress: Double
    public let lastPlayedAt: Date?

    /// Any position on an unwatched video means "in progress" — the card shows
    /// a resume pill and every route into the player resumes from it. There is
    /// no threshold to reimplement client-side.
    public var isInProgress: Bool { !watched && position > 0 }

    public init(
        id: String,
        title: String,
        channel: VideoChannelRef,
        thumbUrl: String,
        duration: Double,
        published: Date? = nil,
        downloaded: Date? = nil,
        type: VideoKind = .video,
        subtitleLangs: [String] = [],
        hasAutoSubtitles: Bool = false,
        watched: Bool = false,
        position: Double = 0,
        progress: Double = 0,
        lastPlayedAt: Date? = nil
    ) {
        self.id = id
        self.title = title
        self.channel = channel
        self.thumbUrl = thumbUrl
        self.duration = duration
        self.published = published
        self.downloaded = downloaded
        self.type = type
        self.subtitleLangs = subtitleLangs
        self.hasAutoSubtitles = hasAutoSubtitles
        self.watched = watched
        self.position = position
        self.progress = progress
        self.lastPlayedAt = lastPlayedAt
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = try c.decode(String.self, forKey: .title)
        channel = try c.decode(VideoChannelRef.self, forKey: .channel)
        thumbUrl = try c.decode(.thumbUrl, or: "")
        duration = try c.decode(.duration, or: 0)
        published = try c.decodeIfPresent(Date.self, forKey: .published)
        downloaded = try c.decodeIfPresent(Date.self, forKey: .downloaded)
        type = try c.decode(.type, or: VideoKind.video)
        subtitleLangs = try c.decode(.subtitleLangs, or: [])
        hasAutoSubtitles = try c.decode(.hasAutoSubtitles, or: false)
        watched = try c.decode(.watched, or: false)
        position = try c.decode(.position, or: 0)
        progress = try c.decode(.progress, or: 0)
        lastPlayedAt = try c.decodeIfPresent(Date.self, forKey: .lastPlayedAt)
    }
}

public enum SubtitleSource: String, Codable, Sendable {
    case user
    case auto
}

public struct SubtitleTrack: Codable, Sendable, Hashable {
    public let lang: String
    public let source: SubtitleSource
    public let url: String

    public init(lang: String, source: SubtitleSource, url: String) {
        self.lang = lang
        self.source = source
        self.url = url
    }
}

public struct SponsorSegment: Codable, Sendable, Hashable {
    public let category: String
    public let start: Double
    public let end: Double

    public init(category: String, start: Double, end: Double) {
        self.category = category
        self.start = start
        self.end = end
    }
}

/// One source rendition of the archived file, as TubeArchivist parsed it.
/// Flimm never re-muxes, so this describes what `mediaUrl` actually contains.
///
/// Use `codec` to decide whether `mediaUrl` plays directly in AVFoundation:
/// H.264 (`avc1`) video with AAC (`mp4a`) audio always does; VP9 (`vp09`) and
/// AV1 (`av01`) video, or Opus audio, are device-dependent.
public struct MediaStream: Codable, Sendable, Hashable {
    public enum Kind: String, Codable, Sendable {
        case video
        case audio
    }

    public let type: Kind
    public let codec: String
    public let width: Int
    public let height: Int
    public let bitrate: Int

    public init(type: Kind, codec: String, width: Int = 0, height: Int = 0, bitrate: Int = 0) {
        self.type = type
        self.codec = codec
        self.width = width
        self.height = height
        self.bitrate = bitrate
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decode(Kind.self, forKey: .type)
        codec = try c.decode(.codec, or: "")
        width = try c.decode(.width, or: 0)
        height = try c.decode(.height, or: 0)
        bitrate = try c.decode(.bitrate, or: 0)
    }

    /// True for the combination AVFoundation is guaranteed to decode.
    public var isNativelyPlayable: Bool {
        switch type {
        case .video: codec.hasPrefix("avc1") || codec.hasPrefix("avc3") || codec.hasPrefix("hvc1") || codec.hasPrefix("hev1")
        case .audio: codec.hasPrefix("mp4a") || codec.hasPrefix("alac")
        }
    }
}

public struct VideoStats: Codable, Sendable, Hashable {
    public let views: Int
    public let likes: Int

    public init(views: Int = 0, likes: Int = 0) {
        self.views = views
        self.likes = likes
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        views = try c.decode(.views, or: 0)
        likes = try c.decode(.likes, or: 0)
    }
}

/// A playlist the video belongs to, as listed on the video detail.
public struct VideoPlaylistRef: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let position: Int
    public let count: Int

    public init(id: String, name: String, position: Int, count: Int) {
        self.id = id
        self.name = name
        self.position = position
        self.count = count
    }
}

/// `GET /videos/{id}` — everything in ``VideoSummary`` plus the detail fields.
public struct Video: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let title: String
    /// The detail carries the full channel object, not the list stub.
    public let channel: ChannelSummary
    public let thumbUrl: String
    public let duration: Double
    public let published: Date?
    public let downloaded: Date?
    public let type: VideoKind
    public let subtitleLangs: [String]
    public let hasAutoSubtitles: Bool
    public let watched: Bool
    public let position: Double
    public let progress: Double
    public let lastPlayedAt: Date?

    public let description: String
    public let height: Int
    public let mediaUrl: String
    /// Derived on first request and cached server-side; Opus in WebM, which
    /// browsers play but AVFoundation cannot decode. Native clients use
    /// ``nativeAudioURL`` instead.
    public let audioUrl: String
    /// The same audio, re-encoded to AAC in MP4 — the rendition AVFoundation
    /// can actually play. Optional: the field arrives with a later backend
    /// release, so a client built against it must not break on a server
    /// without it. Prefer ``nativeAudioURL`` over reading this directly.
    public let audioAacURL: String?
    public let youtubeUrl: String
    /// Source renditions. Optional: the field arrives with a later backend
    /// release, so a client built against it must not break on a server
    /// without it.
    public let streams: [MediaStream]?
    public let subtitles: [SubtitleTrack]
    public let sponsorblock: [SponsorSegment]
    public let stats: VideoStats
    public let tags: [String]
    public let playlists: [VideoPlaylistRef]

    public var isInProgress: Bool { !watched && position > 0 }

    /// The audio-only rendition AVFoundation can actually decode. `audioUrl`
    /// (Opus in WebM) never plays here, so every native audio-only path —
    /// music playlists and the codec-gate fallback alike — must go through
    /// this instead of `audioUrl`. `nil` means the field is missing (an older
    /// backend) or empty, not just that decoding failed.
    public var nativeAudioURL: String? {
        guard let audioAacURL, !audioAacURL.isEmpty else { return nil }
        return audioAacURL
    }

    /// The stub form, for the places that take a list item.
    public var summary: VideoSummary {
        VideoSummary(
            id: id,
            title: title,
            channel: VideoChannelRef(id: channel.id, name: channel.name, thumbUrl: channel.thumbUrl),
            thumbUrl: thumbUrl,
            duration: duration,
            published: published,
            downloaded: downloaded,
            type: type,
            subtitleLangs: subtitleLangs,
            hasAutoSubtitles: hasAutoSubtitles,
            watched: watched,
            position: position,
            progress: progress,
            lastPlayedAt: lastPlayedAt
        )
    }

    /// `.convertFromSnakeCase` turns `audio_aac_url` into `audioAacUrl` (a
    /// lowercase `rl`, not the `URL` acronym form used everywhere else in
    /// this file), so the property needs an explicit raw value or the key
    /// silently fails to match and `audioAacURL` decodes as `nil` on every
    /// server, gated or not.
    private enum CodingKeys: String, CodingKey {
        case id, title, channel, thumbUrl, duration, published, downloaded, type
        case subtitleLangs, hasAutoSubtitles, watched, position, progress, lastPlayedAt
        case description, height, mediaUrl, audioUrl
        case audioAacURL = "audioAacUrl"
        case youtubeUrl, streams, subtitles, sponsorblock, stats, tags, playlists
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = try c.decode(String.self, forKey: .title)
        channel = try c.decode(ChannelSummary.self, forKey: .channel)
        thumbUrl = try c.decode(.thumbUrl, or: "")
        duration = try c.decode(.duration, or: 0)
        published = try c.decodeIfPresent(Date.self, forKey: .published)
        downloaded = try c.decodeIfPresent(Date.self, forKey: .downloaded)
        type = try c.decode(.type, or: VideoKind.video)
        subtitleLangs = try c.decode(.subtitleLangs, or: [])
        hasAutoSubtitles = try c.decode(.hasAutoSubtitles, or: false)
        watched = try c.decode(.watched, or: false)
        position = try c.decode(.position, or: 0)
        progress = try c.decode(.progress, or: 0)
        lastPlayedAt = try c.decodeIfPresent(Date.self, forKey: .lastPlayedAt)
        description = try c.decode(.description, or: "")
        height = try c.decode(.height, or: 0)
        mediaUrl = try c.decode(.mediaUrl, or: "")
        audioUrl = try c.decode(.audioUrl, or: "")
        audioAacURL = try c.decodeIfPresent(String.self, forKey: .audioAacURL)
        youtubeUrl = try c.decode(.youtubeUrl, or: "")
        streams = try c.decodeIfPresent([MediaStream].self, forKey: .streams)
        subtitles = try c.decode(.subtitles, or: [])
        sponsorblock = try c.decode(.sponsorblock, or: [])
        stats = try c.decode(.stats, or: VideoStats())
        tags = try c.decode(.tags, or: [])
        playlists = try c.decode(.playlists, or: [])
    }
}

/// `POST /videos/{id}/progress` response.
public struct ProgressResult: Codable, Sendable, Hashable {
    public let position: Double
    public let watched: Bool

    public init(position: Double, watched: Bool) {
        self.position = position
        self.watched = watched
    }
}

/// `GET /videos/{id}/comments` is a TubeArchivist passthrough and is not
/// pinned down by the contract, so every field is optional and the raw
/// TubeArchivist key names are kept.
public struct VideoComment: Codable, Sendable, Hashable, Identifiable {
    public let commentId: String?
    public let commentText: String?
    public let commentAuthor: String?
    public let commentAuthorId: String?
    public let commentLikecount: Int?
    public let commentTimeText: String?
    public let commentTimestamp: Double?
    public let commentReplies: [VideoComment]?

    public var id: String { commentId ?? UUID().uuidString }
}
