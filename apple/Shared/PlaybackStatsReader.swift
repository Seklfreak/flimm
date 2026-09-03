import AVFoundation
import FlimmKit

/// Reads a live `AVPlayer` into ``PlaybackStats``.
///
/// It sits here rather than in FlimmKit because FlimmKit deliberately knows
/// nothing about AVFoundation, and here rather than in either app because the
/// phone and the television must not measure themselves differently — the
/// panel the companion draws for the Apple TV and the panel the phone draws
/// for itself are the same panel, and would be worthless if the two ends
/// counted a buffer or a dropped frame in different ways.
///
/// Everything is a reading. Nothing here decides, works out or smooths
/// anything: the values come from the item's own properties and its access
/// log, and the ones AVFoundation will not give up are `nil` rather than a
/// figure this file invented.
enum PlaybackStatsReader {

    /// Everything the player itself cannot be asked for: what the gate chose,
    /// why, and where the derivations stand. Each model knows its own.
    struct Input {
        var video: Video?
        var reason: PlaybackReason
        var kind: DeliveryKind
        /// What the player was handed.
        var url: String
        var rendition: PlaybackStats.Rendition?
        var preview: PlaybackStats.Preview
        var loudness: PlaybackStats.Loudness
        /// Where this playback began — the resume point, or 0.
        var startedAt: Double
        var device: DeviceCapabilities
    }

    /// The whole block: the player's own counters, plus what the model knows.
    @MainActor
    static func stats(player: AVPlayer?, _ input: Input) -> PlaybackStats {
        let sourceStreams = (input.video?.streams ?? []).filter { $0.type == .video }
        return PlaybackStats(
            delivery: PlaybackStats.Delivery(
                kind: input.kind,
                reason: input.reason,
                sourceHeight: sourceStreams.map(\.height).max() ?? 0,
                sourceCodec: sourceStreams.first?.codec ?? "",
                rendition: input.rendition,
                url: input.url
            ),
            derived: PlaybackStats.Derived(preview: input.preview, loudness: input.loudness),
            player: readings(
                player: player,
                duration: input.video?.duration ?? 0,
                startedAt: input.startedAt
            ),
            device: PlaybackStats.DeviceReadings(
                decoders: decoders(of: input.device),
                screenHeight: input.device.screenHeight
            )
        )
    }

    /// The item's own counters.
    @MainActor
    static func readings(player: AVPlayer?, duration: Double, startedAt: Double) -> PlaybackStats.PlayerReadings {
        guard let player, let item = player.currentItem else {
            return PlaybackStats.PlayerReadings(status: "no item", startedAt: startedAt)
        }
        let size = item.presentationSize
        let position = item.currentTime().seconds
        let event = item.accessLog()?.events.last
        return PlaybackStats.PlayerReadings(
            status: status(of: item),
            likelyToKeepUp: item.isPlaybackLikelyToKeepUp,
            pictureWidth: Int(size.width),
            pictureHeight: Int(size.height),
            bufferAhead: bufferAhead(of: item),
            // The access log counts frames dropped over the whole item and
            // never a total, so this is a count and not a proportion; -1 is
            // its way of saying it has not counted.
            droppedFrames: event.map(\.numberOfDroppedVideoFrames).flatMap { $0 < 0 ? nil : $0 },
            observedBitrate: event.map(\.observedBitrate).flatMap { $0 > 0 ? $0 : nil },
            position: position.isFinite ? position : 0,
            duration: itemDuration(item) ?? duration,
            startedAt: startedAt,
            volume: Double(player.volume),
            muted: player.isMuted
        )
    }

    /// Seconds of contiguous buffer ahead of the playhead.
    ///
    /// Only the range the playhead is *in* counts. A player that has loaded two
    /// minutes somewhere else on the timeline has nothing to play, and a
    /// number that says otherwise is worse than no number.
    private static func bufferAhead(of item: AVPlayerItem) -> Double? {
        let now = item.currentTime().seconds
        guard now.isFinite else { return nil }
        for range in item.loadedTimeRanges.map(\.timeRangeValue) {
            let start = range.start.seconds
            let end = (range.start + range.duration).seconds
            guard start.isFinite, end.isFinite else { continue }
            if now >= start - 0.1, now <= end { return max(0, end - now) }
        }
        return 0
    }

    private static func itemDuration(_ item: AVPlayerItem) -> Double? {
        let seconds = item.duration.seconds
        return seconds.isFinite && seconds > 0 ? seconds : nil
    }

    private static func status(of item: AVPlayerItem) -> String {
        switch item.status {
        case .readyToPlay: "ready to play"
        case .failed: "failed"
        case .unknown: "unknown"
        @unknown default: "unknown"
        }
    }

    /// What this device admits to decoding in hardware.
    ///
    /// H.264 is not asked about: nothing that runs this app lacks it, and a
    /// list that left it out would read as a device that cannot play anything.
    private static func decoders(of device: DeviceCapabilities) -> [String] {
        var out = ["H.264"]
        if device.decodesHEVC { out.append("HEVC") }
        if device.decodesAV1 { out.append("AV1") }
        if device.decodesVP9 { out.append("VP9") }
        return out
    }
}
