import AVFoundation
import FlimmKit

/// What the phone's player reports about itself, for the playback stats panel.
///
/// An extension, and not only because the class above is at the size a class
/// of that kind should stop growing at: the panel is a separate concern from
/// playing a video, and every value here is a *reading* of state the model was
/// already keeping for its own reasons. Nothing in playback consults any of it.
extension WatchModel {
    /// The rendition row of the playback stats panel, when a rendition is what
    /// is playing. Every value is one this model was already keeping to drive
    /// the preparing overlay — the panel reads them, it does not ask for more.
    var statsRendition: PlaybackStats.Rendition? {
        guard usingCompatibleRendition else { return nil }
        return PlaybackStats.Rendition(
            height: activeVariant?.height ?? 0,
            codec: activeVariant?.codec.rawValue ?? "",
            state: compatibleState ?? .unknown,
            progress: compatibleProgress,
            preparing: isPreparingCompatible
        )
    }

    /// What this player is doing, assembled for the panel.
    ///
    /// The scrub preview is the view's to know — it owns the sheet, and only
    /// while the player is on screen — so it is passed in rather than reached
    /// for. See ``ScrubPreviewState``.
    func stats(preview: PlaybackStats.Preview) -> PlaybackStats {
        PlaybackStatsReader.stats(
            player: engine.player,
            PlaybackStatsReader.Input(
                video: video,
                reason: playbackReason,
                kind: deliveryKind,
                url: mediaPath,
                rendition: statsRendition,
                preview: preview,
                loudness: PlaybackStats.Loudness(
                    enabled: prefs.normalizeLoudness,
                    info: services.loudnessInfo
                ),
                startedAt: resumedFrom ?? 0,
                device: .current
            )
        )
    }
}
