import Foundation

/// One player, saying what it is playing.
///
/// The same type in both directions: a player publishes it (`PUT
/// /playback/sessions/{id}`) and a controller reads it back in the list. The
/// server owns `id` — it comes from the URL — and stamps `updatedAt` itself, so
/// what a publisher puts in those two fields is ignored. Keeping one type is
/// what stops the two ends drifting a field apart.
///
/// Every field is decoded tolerantly. A controller talking to a server older
/// than one of these fields must still see the session rather than fail the
/// whole poll over a key that is not there.
public struct RemoteSession: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    /// What the screen is called — `UIDevice.current.name`, so it is the name
    /// the viewer gave the Apple TV in Settings.
    public let device: String
    /// The client kind: `tvos`, `ios`, `web`. For picking an icon, never for
    /// deciding what a session can be asked to do.
    public let platform: String
    public let videoId: String
    public let title: String
    public let channelName: String
    public let thumbUrl: String
    /// Where playback was when this was published. It is a fix, not a clock —
    /// run it forward with ``RemoteClock`` rather than showing it directly, or
    /// the controller's scrubber sits still between heartbeats.
    public let position: Double
    public let duration: Double
    public let paused: Bool
    /// The rate the position advances at, so a controller's own clock runs at
    /// the speed the television is actually playing.
    public let speed: Double
    public let audioOnly: Bool
    /// Whether the player has somewhere to step to. The player worked this out
    /// from its own context (`/videos/{id}/nav`), which is the only place it is
    /// known — a controller must not try to derive it.
    public let canNext: Bool
    public let canPrevious: Bool
    /// Server time, not the publisher's. Present so a session's age can be
    /// reasoned about at all; ``RemoteClock`` deliberately does *not* use it,
    /// because a phone's clock and a server's clock are not the same clock.
    public let updatedAt: Date

    public init(
        id: String = "",
        device: String = "",
        platform: String = "",
        videoId: String,
        title: String = "",
        channelName: String = "",
        thumbUrl: String = "",
        position: Double = 0,
        duration: Double = 0,
        paused: Bool = false,
        speed: Double = 1,
        audioOnly: Bool = false,
        canNext: Bool = false,
        canPrevious: Bool = false,
        updatedAt: Date = Date(timeIntervalSince1970: 0)
    ) {
        self.id = id
        self.device = device
        self.platform = platform
        self.videoId = videoId
        self.title = title
        self.channelName = channelName
        self.thumbUrl = thumbUrl
        self.position = position
        self.duration = duration
        self.paused = paused
        self.speed = speed
        self.audioOnly = audioOnly
        self.canNext = canNext
        self.canPrevious = canPrevious
        self.updatedAt = updatedAt
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(.id, or: "")
        device = try c.decode(.device, or: "")
        platform = try c.decode(.platform, or: "")
        videoId = try c.decode(String.self, forKey: .videoId)
        title = try c.decode(.title, or: "")
        channelName = try c.decode(.channelName, or: "")
        thumbUrl = try c.decode(.thumbUrl, or: "")
        position = try c.decode(.position, or: 0)
        duration = try c.decode(.duration, or: 0)
        paused = try c.decode(.paused, or: false)
        // A server that says nothing about speed is playing at 1×; a zero
        // would stop a controller's clock dead.
        speed = try c.decode(.speed, or: 1)
        audioOnly = try c.decode(.audioOnly, or: false)
        canNext = try c.decode(.canNext, or: false)
        canPrevious = try c.decode(.canPrevious, or: false)
        updatedAt = try c.decode(.updatedAt, or: Date(timeIntervalSince1970: 0))
    }

    /// A copy with new playback numbers, for a publisher that only has to say
    /// the clock moved.
    public func moved(position: Double, paused: Bool, speed: Double) -> RemoteSession {
        RemoteSession(
            id: id, device: device, platform: platform, videoId: videoId,
            title: title, channelName: channelName, thumbUrl: thumbUrl,
            position: position, duration: duration, paused: paused, speed: speed,
            audioOnly: audioOnly, canNext: canNext, canPrevious: canPrevious,
            updatedAt: updatedAt
        )
    }
}

/// `GET /playback/sessions`: every screen of this account's that is playing,
/// and the version to come back with.
///
/// The version is the whole discovery mechanism. Handing it back as `since`
/// turns the same request into a long poll that answers the moment anything
/// changes — a session appearing, moving, pausing or lapsing — so a controller
/// never polls on a timer and never misses a change between two polls.
public struct RemoteSessions: Codable, Sendable, Hashable {
    public let sessions: [RemoteSession]
    public let version: UInt64

    public init(sessions: [RemoteSession] = [], version: UInt64 = 0) {
        self.sessions = sessions
        self.version = version
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessions = try c.decode(.sessions, or: [])
        version = try c.decode(.version, or: 0)
    }
}

/// One instruction for a player.
///
/// Nothing acknowledges a command. The player applies what it can and publishes
/// its state, and the state is the acknowledgement — a controller that sent
/// "pause" and then sees a paused session knows it landed, without a second
/// protocol saying so.
public struct RemoteCommand: Codable, Sendable, Hashable, Identifiable {
    /// What a player knows how to do. Decoded as a string rather than as this
    /// enum, so a command a newer controller invented is skipped by an older
    /// player instead of failing its whole poll.
    public enum Action: String, Sendable, CaseIterable {
        case play, pause, seek, skip, next, previous
    }

    /// Server-assigned, and strictly increasing per session: it is the cursor
    /// a player comes back with, which is what stops the same seek being
    /// applied on every poll.
    public let seq: UInt64
    public let kind: String
    /// Seek target, in seconds. Only meaningful for ``Action/seek``.
    public let position: Double
    /// How far to move from wherever playback actually is, signed. Only for
    /// ``Action/skip`` — which exists apart from `seek` because a controller
    /// pressing ±10s does not know where the television is to within the round
    /// trip, and a seek computed from a projected clock would land slightly
    /// wrong every time.
    public let delta: Double

    public var id: UInt64 { seq }
    public var action: Action? { Action(rawValue: kind) }

    public init(seq: UInt64 = 0, kind: String, position: Double = 0, delta: Double = 0) {
        self.seq = seq
        self.kind = kind
        self.position = position
        self.delta = delta
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(.seq, or: 0)
        kind = try c.decode(.kind, or: "")
        position = try c.decode(.position, or: 0)
        delta = try c.decode(.delta, or: 0)
    }

    public static let play = RemoteCommand(kind: Action.play.rawValue)
    public static let pause = RemoteCommand(kind: Action.pause.rawValue)
    public static let next = RemoteCommand(kind: Action.next.rawValue)
    public static let previous = RemoteCommand(kind: Action.previous.rawValue)
    public static func seek(to seconds: Double) -> RemoteCommand {
        RemoteCommand(kind: Action.seek.rawValue, position: max(0, seconds))
    }
    public static func skip(_ seconds: Double) -> RemoteCommand {
        RemoteCommand(kind: Action.skip.rawValue, delta: seconds)
    }
}

/// `GET /playback/sessions/{id}/commands`: what a player has not seen yet.
///
/// `cursor` must be adopted whether or not anything came with it. A session
/// whose backlog overflowed has commands the player will never receive, and a
/// cursor that only moved on delivery would ask for them for ever.
public struct RemoteCommandBatch: Codable, Sendable, Hashable {
    public let commands: [RemoteCommand]
    public let cursor: UInt64

    public init(commands: [RemoteCommand] = [], cursor: UInt64 = 0) {
        self.commands = commands
        self.cursor = cursor
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        commands = try c.decode(.commands, or: [])
        cursor = try c.decode(.cursor, or: 0)
    }
}
