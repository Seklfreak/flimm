import FlimmKit
import SwiftUI

/// The transport chrome drawn over the video layer.
///
/// Everything is a plain SwiftUI control over `AVPlayerLayer`, which is what
/// buys the chapter ticks and SponsorBlock tints on the scrubber.
/// Sizes for the transport chrome.
///
/// A glyph is not a target: an `Image` in a `Button` is only as tappable as
/// the icon is big, which left most of these controls around 17–26pt against
/// Apple's 44pt minimum. The player is the worst place to miss — the video
/// carries on while the viewer stabs at it — so every control gets a real
/// target whatever its icon, and the iPad gets more of both: a bigger surface
/// held further away, usually one-handed at the edge of reach.
/// Where the chrome along the bottom edge begins, in the player stage's
/// coordinate space, so subtitles can clear whatever is actually drawn there.
///
/// It is measured rather than assumed because the bar is not one height: it is
/// taller on an iPad than on a phone, and taller again while a "jump to the
/// highlight" pill is up. A constant can only be right for one of those, and
/// the others are a caption sitting on the scrubber. 0 means nothing is drawn.
struct PlayerChromeTopKey: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        value = max(value, nextValue())
    }
}

/// The coordinate space of the video itself, which is what a cue's position is
/// measured against.
enum PlayerStage {
    static let space = "flimm.player.stage"
}

enum PlayerMetrics {
    static func hitTarget(regular: Bool) -> CGFloat { regular ? 52 : 44 }
    static func barIcon(regular: Bool) -> CGFloat { regular ? 21 : 17 }
    static func transportIcon(regular: Bool) -> CGFloat { regular ? 32 : 26 }
    static func playIcon(regular: Bool) -> CGFloat { regular ? 48 : 40 }
    static func playTarget(regular: Bool) -> CGFloat { regular ? 68 : 56 }
    /// The scrubber's drawn height. Its *touch* height is the target above,
    /// added without moving the bar (see ``ScrubberView``).
    static let scrubberBar: CGFloat = 22
}

extension View {
    /// A tappable area of at least `side`, centred on whatever it wraps.
    /// `contentShape` is what makes the padding around a glyph hit-testable.
    func playerHitTarget(_ side: CGFloat) -> some View {
        frame(minWidth: side, minHeight: side)
            .contentShape(Rectangle())
    }
}

struct PlayerControls: View {
    let model: WatchModel
    var isFullScreen = false
    let onClose: () -> Void
    let onToggleFullScreen: () -> Void
    let scrubPreview: ScrubPreviewState

    @Binding var isVisible: Bool
    @Environment(\.horizontalSizeClass) private var sizeClass

    /// The iPad (and an iPhone in landscape) gets the roomier set.
    private var regular: Bool { sizeClass == .regular }
    private var hit: CGFloat { PlayerMetrics.hitTarget(regular: regular) }

    var body: some View {
        ZStack {
            if isVisible {
                LinearGradient(
                    colors: [.black.opacity(0.55), .clear, .black.opacity(0.65)],
                    startPoint: .top,
                    endPoint: .bottom
                )
                .allowsHitTesting(false)
                VStack {
                    topBar
                    Spacer(minLength: 0)
                    centreControls
                    Spacer(minLength: 0)
                    bottomBar
                        .background(
                            GeometryReader { geo in
                                Color.clear.preference(
                                    key: PlayerChromeTopKey.self,
                                    value: geo.frame(in: .named(PlayerStage.space)).minY
                                )
                            }
                        )
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
            }
            if model.engine.isBuffering {
                ProgressView().tint(.white).scaleEffect(1.3)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: isVisible)
    }

    // MARK: - Bars

    private var topBar: some View {
        HStack(spacing: 14) {
            Button(action: onClose) {
                Image(systemName: isFullScreen ? "arrow.down.right.and.arrow.up.left" : "chevron.down")
                    .playerHitTarget(hit)
            }
            .accessibilityLabel(isFullScreen ? "Exit full screen" : "Close player")
            Spacer(minLength: 0)
            Button {
                Task { await model.toggleAudioOnly() }
            } label: {
                Image(systemName: model.audioOnly ? "headphones" : "headphones.slash")
                    .playerHitTarget(hit)
            }
            .accessibilityLabel(model.audioOnly ? "Switch to video" : "Audio only")
            if model.engine.isPiPPossible && !model.audioOnly {
                Button {
                    model.engine.togglePiP()
                } label: {
                    Image(systemName: "pip.enter")
                        .playerHitTarget(hit)
                }
                .accessibilityLabel("Picture in Picture")
            }
            optionsMenu
        }
        .font(.system(size: PlayerMetrics.barIcon(regular: regular), weight: .semibold))
        .foregroundStyle(.white)
    }

    private var optionsMenu: some View {
        Menu {
            if model.usingCompatibleRendition {
                // Informational: which rendition is playing, said where it is
                // cheap to say it. The picker below is where it is changed.
                Button {} label: {
                    Label(VideoQuality.renditionHint(model.activeVariant), systemImage: "wand.and.rays")
                }
                .disabled(true)
            }
            qualityMenu
            Picker("Speed", selection: speedBinding) {
                ForEach(PlaybackSpeeds.all, id: \.self) { speed in
                    Text(Fmt.speed(speed)).tag(speed)
                }
            }
            subtitleMenu
            Button {
                model.toggleMute()
            } label: {
                Label(
                    model.engine.isMuted ? "Unmute" : "Mute",
                    systemImage: model.engine.isMuted ? "speaker.slash" : "speaker.wave.2"
                )
            }
            if model.hasContext {
                Button {
                    Task { await model.reshuffle() }
                } label: {
                    Label("Shuffle", systemImage: "shuffle")
                }
            }
            if !model.audioOnly {
                Button {
                    Task { await model.setWatched(!model.isWatched) }
                } label: {
                    Label(model.isWatched ? "Mark unseen" : "Mark seen", systemImage: "checkmark.circle")
                }
            }
        } label: {
            Image(systemName: "ellipsis.circle")
                .playerHitTarget(hit)
        }
    }

    /// The quality picker.
    ///
    /// It is a per-device choice, not a server preference, and it applies from
    /// here on rather than to this video alone. Auto is the archived file when
    /// this device decodes it — free and full quality — and otherwise the
    /// tallest rendition the screen can show; picking a height explicitly wins
    /// even over a playable archive, because "720p" is a request for less data.
    @ViewBuilder
    private var qualityMenu: some View {
        if !model.audioOnly, !model.qualityLadder.isEmpty {
            Menu("Quality") {
                qualityRow(VideoQuality.label(.auto), preference: .auto)
                if model.archivePlaysNatively {
                    // What Auto plays here, named rather than left implicit.
                    // Not a choice of its own: Auto is how you ask for it.
                    Button {} label: { Text(VideoQuality.sourceLabel(for: model.video)) }
                        .disabled(true)
                }
                ForEach(model.qualityLadder) { variant in
                    qualityRow(label(for: variant), preference: .height(variant.height))
                }
                // A height chosen on another video and not offered here still
                // shows, so the picker never looks like it lost the setting.
                if let height = model.videoQuality.height,
                   !model.qualityLadder.contains(where: { $0.height == height }) {
                    qualityRow("\(height)p · not offered", preference: .height(height))
                }
            }
        }
    }

    private func qualityRow(_ title: String, preference: QualityPreference) -> some View {
        Button {
            Task { await model.setVideoQuality(preference) }
        } label: {
            if model.videoQuality == preference {
                Label(title, systemImage: "checkmark")
            } else {
                Text(title)
            }
        }
    }

    /// `1080p`, `2160p · HEVC · ready`, `720p · preparing` — the state only
    /// when there is something to say about it.
    private func label(for variant: HLSVariant) -> String {
        let label = VideoQuality.label(variant)
        guard let hint = VideoQuality.stateHint(variant.state) else { return label }
        return "\(label) · \(hint)"
    }

    @ViewBuilder
    private var subtitleMenu: some View {
        if let tracks = model.video?.subtitles, !tracks.isEmpty {
            Menu("Subtitles") {
                Picker("Subtitles", selection: subtitleBinding) {
                    Text("Off").tag(Prefs.subtitlesOff)
                    ForEach(tracks, id: \.self) { track in
                        Text(track.source == .auto ? "\(Fmt.langName(track.lang)) (auto)" : Fmt.langName(track.lang))
                            .tag(track.lang)
                    }
                }
            }
        }
    }

    private var centreControls: some View {
        HStack(spacing: 28) {
            if model.hasContext {
                Button {
                    Task { await model.goPrevious() }
                } label: {
                    Image(systemName: "backward.end.fill")
                        .playerHitTarget(hit)
                }
                .disabled(!model.canGoPrevious)
                .opacity(model.canGoPrevious ? 1 : 0.35)
            }
            Button { model.skip(by: -10) } label: {
                Image(systemName: "gobackward.10")
                    .playerHitTarget(hit)
            }
            Button { model.togglePlay() } label: {
                Image(systemName: model.engine.isPlaying ? "pause.fill" : "play.fill")
                    .font(.system(size: PlayerMetrics.playIcon(regular: regular), weight: .bold))
                    .playerHitTarget(PlayerMetrics.playTarget(regular: regular))
            }
            Button { model.skip(by: 10) } label: {
                Image(systemName: "goforward.10")
                    .playerHitTarget(hit)
            }
            if model.hasContext {
                Button {
                    Task { await model.goNext() }
                } label: {
                    Image(systemName: "forward.end.fill")
                        .playerHitTarget(hit)
                }
                .disabled(!model.canGoNext && model.upNext.isEmpty)
                .opacity(model.canGoNext || !model.upNext.isEmpty ? 1 : 0.35)
            }
        }
        .font(.system(size: PlayerMetrics.transportIcon(regular: regular), weight: .semibold))
        .foregroundStyle(.white)
    }

    private var bottomBar: some View {
        VStack(spacing: 4) {
            if let highlight = SponsorRules.highlightToOffer(
                at: model.engine.currentTime, in: model.video?.sponsorblock ?? []
            ) {
                HighlightButton(start: highlight.start) { model.seek(to: highlight.start) }
                    .frame(maxWidth: .infinity, alignment: .trailing)
            }
            ScrubberView(
                currentTime: model.engine.currentTime,
                duration: model.engine.duration,
                chapters: model.chapters,
                sponsors: model.video?.sponsorblock ?? [],
                preview: scrubPreview.tiles,
                previewSheet: scrubPreview.sheet,
                onScrub: { _ in },
                onCommit: { model.seek(to: $0) }
            )
            HStack {
                Text(Fmt.duration(model.engine.currentTime))
                Spacer()
                if let index = chapterTitle {
                    Text(index)
                        .lineLimit(1)
                        .foregroundStyle(.white.opacity(0.75))
                    Spacer()
                }
                Text(Fmt.duration(model.engine.duration))
                Button(action: onToggleFullScreen) {
                    Image(systemName: isFullScreen ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right")
                        // Its own size, not the row's `.caption`, which made
                        // this the smallest target on the screen.
                        .font(.system(size: PlayerMetrics.barIcon(regular: regular), weight: .semibold))
                        .playerHitTarget(hit)
                }
                .accessibilityLabel("Toggle full screen")
            }
            .font(.caption.monospacedDigit().weight(.semibold))
            .foregroundStyle(.white)
        }
    }

    private var chapterTitle: String? {
        guard model.activeChapter >= 0, model.activeChapter < model.chapters.count else { return nil }
        return model.chapters[model.activeChapter].title
    }

    private var speedBinding: Binding<Double> {
        Binding(
            get: { model.prefs.playbackSpeed },
            set: { value in Task { await model.setSpeed(value) } }
        )
    }

    private var subtitleBinding: Binding<String> {
        Binding(
            get: { model.prefs.subtitleLang },
            set: { value in Task { await model.setSubtitleLanguage(value) } }
        )
    }
}

/// "Resumed from 12:31 · Start over" — the chip the design puts in the
/// top-left, because resume happens automatically and should be undoable.
/// "Skip the intro": what a category set to *ask* offers instead of jumping.
/// The label names the category, because "skip" alone does not say what is
/// about to be skipped — and the viewer chose to be asked precisely because
/// they sometimes want that section.
struct SkipSegmentButton: View {
    let category: String
    let skip: () -> Void

    var body: some View {
        Button(action: skip) {
            HStack(spacing: 6) {
                Text("Skip \(SponsorRules.label(category).lowercased())")
                    .font(.footnote.weight(.semibold))
                Image(systemName: "forward.end.fill")
                    .font(.caption2)
            }
            .foregroundStyle(.white)
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
            .background(Palette.overlay, in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Skip \(SponsorRules.label(category).lowercased())")
    }
}

/// "Jump to the highlight": the one thing a SponsorBlock point of interest is
/// for. Never automatic — a highlight is offered, never taken for the viewer —
/// and offered whatever the skip preference says, because this is a tap, not
/// a skip.
struct HighlightButton: View {
    let start: Double
    let onJump: () -> Void

    var body: some View {
        Button(action: onJump) {
            HStack(spacing: 6) {
                Image(systemName: "sparkles")
                Text("Jump to the highlight")
                Text(Fmt.duration(start))
                    .foregroundStyle(.white.opacity(0.7))
            }
            .font(.caption.weight(.semibold))
            .foregroundStyle(.white)
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(Palette.overlay, in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Jump to the highlight at \(Fmt.duration(start))")
    }
}

/// The end of the video, said out loud.
///
/// A finished video is a still frame, which is exactly what a paused one looks
/// like; this is the difference between the two, plus the two things a viewer
/// wants at that moment — watch it again, or take what is next. When autoplay
/// takes over there is no card at all: ``PlaybackEnd`` decides, once, for both
/// Apple clients and the web.
struct EndCard: View {
    let next: VideoSummary?
    let onReplay: () -> Void
    let onPlayNext: () -> Void

    var body: some View {
        ZStack {
            Color.black.opacity(0.7)
            VStack(spacing: 12) {
                Label("Finished", systemImage: "checkmark.circle")
                    .font(.caption.weight(.bold))
                    .foregroundStyle(.white.opacity(0.7))
                if let next {
                    Button(action: onPlayNext) {
                        HStack(spacing: 10) {
                            VideoThumbnail(video: next, compact: true)
                                .frame(width: 96, height: 54)
                                .clipped()
                            VStack(alignment: .leading, spacing: 2) {
                                Text("Up next")
                                    .font(.caption2.weight(.bold))
                                    .foregroundStyle(.white.opacity(0.55))
                                Text(next.title)
                                    .font(.footnote.weight(.heavy))
                                    .foregroundStyle(.white)
                                    .lineLimit(2)
                                Text(next.channel.name)
                                    .font(.caption2.weight(.semibold))
                                    .foregroundStyle(.white.opacity(0.6))
                            }
                            .multilineTextAlignment(.leading)
                            Spacer(minLength: 0)
                        }
                        .padding(8)
                        .background(Color.white.opacity(0.14), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                    }
                    .buttonStyle(.plain)
                    .frame(maxWidth: 340)
                }
                Button(action: onReplay) {
                    Label("Replay", systemImage: "arrow.counterclockwise")
                        .font(.footnote.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 9)
                        .background(Color.white.opacity(0.16), in: Capsule())
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 20)
        }
    }
}

struct ResumeToast: View {
    let position: Double
    let onStartOver: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Text("Resumed from \(Fmt.duration(position))")
            Divider().frame(height: 12).overlay(Color.white.opacity(0.4))
            Button("Start over", action: onStartOver)
                .fontWeight(.bold)
        }
        .font(.caption.weight(.semibold))
        .foregroundStyle(.white)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(Palette.overlay, in: Capsule())
    }
}
