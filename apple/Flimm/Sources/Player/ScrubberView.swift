import FlimmKit
import SwiftUI

/// The transport bar's timeline: played position, buffered-agnostic track,
/// SponsorBlock tints and chapter boundary ticks, with a draggable thumb.
///
/// Drawing it here rather than using `VideoPlayer`'s built-in controls is the
/// whole reason the player is built on `AVPlayerLayer`.
struct ScrubberView: View {
    let currentTime: Double
    let duration: Double
    let chapters: [Chapter]
    let sponsors: [SponsorSegment]
    let onScrub: (Double) -> Void
    let onCommit: (Double) -> Void

    @State private var dragValue: Double?

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
                    .fill(Color.white.opacity(0.25))
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

                ForEach(Array(ChapterMath.markerFractions(chapters, duration: duration).enumerated()), id: \.offset) { _, mark in
                    Rectangle()
                        .fill(Color.white.opacity(0.85))
                        .frame(width: 2, height: 10)
                        .offset(x: width * mark - 1)
                }

                Circle()
                    .fill(.white)
                    .frame(width: dragValue == nil ? 12 : 18)
                    .offset(x: width * fraction - (dragValue == nil ? 6 : 9))
                    .shadow(radius: 2)
            }
            .frame(height: 22)
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
        .frame(height: 22)
        .animation(.easeOut(duration: 0.12), value: dragValue == nil)
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
