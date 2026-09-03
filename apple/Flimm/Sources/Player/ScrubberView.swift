import FlimmKit
import SwiftUI
import UIKit

/// How a scrubber is coloured.
///
/// The player draws its bar over the picture, where white is the only colour
/// that survives an arbitrary frame behind it. The companion draws the same
/// bar on a page, where white is nothing at all. Only the parts that sit on
/// the background differ — the thumb stays white with its shadow in both, the
/// way a system `Slider`'s knob does in light and dark alike.
struct ScrubberStyle {
    /// The unplayed bar.
    var track: Color
    /// Chapter boundary ticks.
    var marker: Color
    /// The timestamp under the scrub-preview still.
    var label: Color

    static let overVideo = ScrubberStyle(
        track: .white.opacity(0.25),
        marker: .white.opacity(0.85),
        label: .white
    )

    /// On an app background: the companion's transport.
    static let onSurface = ScrubberStyle(
        track: Color.primary.opacity(0.18),
        marker: Color.primary.opacity(0.6),
        label: .primary
    )
}

/// The transport bar's timeline: played position, buffered-agnostic track,
/// SponsorBlock tints and chapter boundary ticks, with a draggable thumb.
///
/// Drawing it here rather than using `VideoPlayer`'s built-in controls is the
/// whole reason the player is built on `AVPlayerLayer`. It is also what the
/// companion scrubs the television with — one scrubber, so a preview still or
/// a chapter tick cannot exist on one screen and not the other.
struct ScrubberView: View {
    let currentTime: Double
    let duration: Double
    let chapters: [Chapter]
    let sponsors: [SponsorSegment]
    /// The scrub-preview stills, empty until the server has derived them.
    var preview: [PreviewTile] = []
    var previewSheet: UIImage?
    var style: ScrubberStyle = .overVideo
    let onScrub: (Double) -> Void
    let onCommit: (Double) -> Void

    @State private var dragValue: Double? = Self.pinnedScrub

    /// Debug door: `FLIMM_SCRUB_AT=<seconds>` parks the bar mid-drag.
    ///
    ///     SIMCTL_CHILD_FLIMM_SCRUB_AT=42 xcrun simctl launch <device> …
    ///
    /// The preview still exists for the length of a touch and not one frame
    /// longer, so it is invisible to a screenshot — and a state nobody can see
    /// is a state nobody checks. This is the only way to look at it without a
    /// finger on the glass, on the player and on the companion alike.
    #if DEBUG
    private static let pinnedScrub = ProcessInfo.processInfo.environment["FLIMM_SCRUB_AT"].flatMap(Double.init)
    #else
    private static let pinnedScrub: Double? = nil
    #endif

    private var fraction: Double {
        guard duration > 0 else { return 0 }
        let value = dragValue ?? currentTime
        return min(max(value / duration, 0), 1)
    }

    var body: some View {
        GeometryReader { geo in
            let width = geo.size.width
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(style.track)
                    .frame(height: 4)

                ForEach(SponsorRules.ranges(sponsors, duration: duration), id: \.self) { range in
                    Rectangle()
                        .fill(SponsorRules.tint(for: range.category).opacity(0.75))
                        .frame(width: max(2, width * range.width), height: 4)
                        .offset(x: width * range.start)
                }

                Capsule()
                    .fill(Palette.accent)
                    .frame(width: width * fraction, height: 4)

                // The highlight is an instant, not a band: a diamond sitting
                // on the bar rather than a tint across it.
                ForEach(Array(SponsorRules.pointFractions(sponsors, duration: duration).enumerated()), id: \.offset) { _, point in
                    Rectangle()
                        .fill(Palette.accent)
                        .frame(width: 7, height: 7)
                        .rotationEffect(.degrees(45))
                        .offset(x: width * point - 3.5)
                }

                ForEach(Array(ChapterMath.markerFractions(chapters, duration: duration).enumerated()), id: \.offset) { _, mark in
                    Rectangle()
                        .fill(style.marker)
                        .frame(width: 2, height: 10)
                        .offset(x: width * mark - 1)
                }

                Circle()
                    .fill(.white)
                    .frame(width: dragValue == nil ? 12 : 18)
                    .offset(x: width * fraction - (dragValue == nil ? 6 : 9))
                    .shadow(radius: 2)
            }
            // The still for wherever the drag is, held above the bar. It is an
            // overlay rather than a row above it so the transport chrome does
            // not change height when a video has previews and another has not.
            .overlay(alignment: .topLeading) { previewOverlay(width: width) }
            // Drawn 22pt tall, dragged over 44: a thin bar is the right look
            // and the wrong target, so the touch area is grown and the layout
            // pulled back by the same amount (below) so nothing moves.
            .frame(height: PlayerMetrics.scrubberBar)
            .padding(.vertical, (44 - PlayerMetrics.scrubberBar) / 2)
            .contentShape(Rectangle())
            .gesture(
                DragGesture(minimumDistance: 0)
                    .onChanged { value in
                        guard duration > 0, width > 0 else { return }
                        let target = min(max(value.location.x / width, 0), 1) * duration
                        dragValue = target
                        onScrub(target)
                    }
                    .onEnded { value in
                        guard duration > 0, width > 0 else { return }
                        let target = min(max(value.location.x / width, 0), 1) * duration
                        dragValue = nil
                        onCommit(target)
                    }
            )
        }
        .frame(height: 44)
        .padding(.vertical, -(44 - PlayerMetrics.scrubberBar) / 2)
        .animation(.easeOut(duration: 0.12), value: dragValue == nil)
        // A drag gesture is invisible to VoiceOver, so the bar has to say what
        // it is and be adjustable by itself. The step is a proportion rather
        // than a fixed ten seconds, which would be a hundred swipes across a
        // long video and a jump across a short one.
        .accessibilityElement()
        .accessibilityLabel("Position")
        .accessibilityValue(Fmt.duration(dragValue ?? currentTime))
        .accessibilityAdjustableAction { direction in
            guard duration > 0 else { return }
            let step = max(5, duration / 20)
            let from = dragValue ?? currentTime
            switch direction {
            case .increment: onCommit(min(from + step, duration))
            case .decrement: onCommit(max(from - step, 0))
            @unknown default: break
            }
        }
    }

    /// The still above the thumb, while a drag is in progress and only then:
    /// a picture that followed the playhead would be a distraction, and this
    /// is the one moment a viewer is asking "what is *there*".
    @ViewBuilder
    private func previewOverlay(width: CGFloat) -> some View {
        if let target = dragValue,
           let tile = ScrubPreview.tile(at: target, in: preview),
           let sheet = previewSheet,
           let still = Self.crop(sheet, to: tile.rect) {
            let size = CGSize(width: Self.previewWidth, height: Self.previewWidth * tile.rect.height / tile.rect.width)
            VStack(spacing: 3) {
                Image(uiImage: still)
                    .resizable()
                    .frame(width: size.width, height: size.height)
                    .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
                    .overlay(
                        RoundedRectangle(cornerRadius: 6, style: .continuous)
                            .strokeBorder(.white.opacity(0.35), lineWidth: 1)
                    )
                Text(Fmt.duration(target))
                    .font(.caption2.monospacedDigit().weight(.semibold))
                    .foregroundStyle(style.label)
            }
            .shadow(radius: 8)
            // Centred on the thumb, but never pushed off either end of the bar.
            .offset(
                x: min(max(width * fraction - size.width / 2, 0), max(width - size.width, 0)),
                y: -(size.height + 24)
            )
            .allowsHitTesting(false)
            .transition(.opacity)
        }
    }

    /// How wide the still is drawn. The sheet's own tiles are 160px, so this
    /// is life size and never upscaled.
    private static let previewWidth: CGFloat = 160

    /// `cropping` returns a view onto the same pixels rather than a copy, so
    /// doing this per drag update costs nothing to speak of.
    private static func crop(_ image: UIImage, to rect: CGRect) -> UIImage? {
        guard let cgImage = image.cgImage?.cropping(to: rect) else { return nil }
        return UIImage(cgImage: cgImage, scale: image.scale, orientation: image.imageOrientation)
    }
}

/// The chapter list under the video. Hidden entirely when there are none.
struct ChapterListView: View {
    let chapters: [Chapter]
    let activeIndex: Int
    let onSeek: (Double) -> Void

    var body: some View {
        if !chapters.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                Text("Chapters")
                    .font(.headline)
                ForEach(Array(chapters.enumerated()), id: \.offset) { index, chapter in
                    Button {
                        onSeek(chapter.start)
                    } label: {
                        HStack(spacing: 10) {
                            Text(Fmt.duration(chapter.start))
                                .font(.caption.monospacedDigit().weight(.semibold))
                                .foregroundStyle(index == activeIndex ? Palette.accent : .secondary)
                                .frame(width: 58, alignment: .leading)
                            Text(chapter.title)
                                .font(.subheadline)
                                .fontWeight(index == activeIndex ? .bold : .regular)
                                .multilineTextAlignment(.leading)
                            Spacer(minLength: 0)
                        }
                        .padding(.vertical, 6)
                        .padding(.horizontal, 10)
                        .background(
                            index == activeIndex ? Palette.raised : Color.clear,
                            in: RoundedRectangle(cornerRadius: 8, style: .continuous)
                        )
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }
}

/// Cue text drawn over the video. `AVPlayer` cannot side-load a WebVTT track
/// that needs an auth header, so the cues are parsed and rendered here — which
/// also gives the size preference something to act on.
struct SubtitleOverlay: View {
    let text: String?
    let size: SubtitleSize

    private var fontSize: CGFloat {
        switch size {
        case .small: 14
        case .medium: 18
        case .large: 24
        }
    }

    var body: some View {
        if let text, !text.isEmpty {
            Text(text)
                .font(.system(size: fontSize, weight: .semibold))
                .foregroundStyle(.white)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(Color.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                .padding(.horizontal, 16)
                .transition(.opacity)
        }
    }
}
