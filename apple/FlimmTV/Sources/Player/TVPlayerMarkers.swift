import AVFoundation
import AVKit
import FlimmKit
import Foundation

/// Turns Flimm's chapters and SponsorBlock segments into the two things the
/// tvOS transport bar understands natively.
///
/// Neither is drawn by us: chapters become an `AVNavigationMarkersGroup` on the
/// player item, which gives the remote's chapter list and the swipe-up
/// thumbnails for free, and sponsors become `interstitialTimeRanges`, which
/// stripes them on the scrubber. Roughly a third of videos have no chapters at
/// all, so an empty list must mean "no chapter UI", never an error.
enum TVPlayerMarkers {
    static func navigationMarkers(for chapters: [Chapter], duration: Double) -> AVNavigationMarkersGroup? {
        guard !chapters.isEmpty else { return nil }
        let groups: [AVTimedMetadataGroup] = chapters.compactMap { chapter in
            let end = chapter.end > chapter.start ? chapter.end : duration
            guard end > chapter.start else { return nil }
            let title = AVMutableMetadataItem()
            title.identifier = .commonIdentifierTitle
            title.value = chapter.title as NSString
            title.extendedLanguageTag = "und"
            let range = CMTimeRange(
                start: CMTime(seconds: chapter.start, preferredTimescale: 600),
                end: CMTime(seconds: end, preferredTimescale: 600)
            )
            return AVTimedMetadataGroup(items: [title], timeRange: range)
        }
        guard !groups.isEmpty else { return nil }
        return AVNavigationMarkersGroup(title: "Chapters", timedNavigationMarkers: groups)
    }

    /// Every SponsorBlock category is striped, not just the skippable ones —
    /// the same split the web client makes between "show it" and "skip it".
    static func interstitials(for segments: [SponsorSegment]) -> [AVInterstitialTimeRange] {
        segments.compactMap { segment in
            guard segment.end > segment.start else { return nil }
            return AVInterstitialTimeRange(timeRange: CMTimeRange(
                start: CMTime(seconds: segment.start, preferredTimescale: 600),
                end: CMTime(seconds: segment.end, preferredTimescale: 600)
            ))
        }
    }
}
