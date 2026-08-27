import FlimmKit
import SwiftUI

/// The transport chrome drawn over the video layer.
///
/// Everything is a plain SwiftUI control over `AVPlayerLayer`, which is what
/// buys the chapter ticks and SponsorBlock tints on the scrubber.
struct PlayerControls: View {
    let model: WatchModel
    var isFullScreen = false
    let onClose: () -> Void
    let onToggleFullScreen: () -> Void

    @Binding var isVisible: Bool

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
            }
            .accessibilityLabel(isFullScreen ? "Exit full screen" : "Close player")
            Spacer(minLength: 0)
            Button {
                Task { await model.toggleAudioOnly() }
            } label: {
                Image(systemName: model.audioOnly ? "headphones" : "headphones.slash")
            }
            .accessibilityLabel(model.audioOnly ? "Switch to video" : "Audio only")
            if model.engine.isPiPPossible && !model.audioOnly {
                Button {
                    model.engine.togglePiP()
                } label: {
                    Image(systemName: "pip.enter")
                }
                .accessibilityLabel("Picture in Picture")
            }
            optionsMenu
        }
        .font(.system(size: 17, weight: .semibold))
        .foregroundStyle(.white)
    }

    private var optionsMenu: some View {
        Menu {
            if model.usingCompatibleRendition {
                // Informational, not a control: the rendition is chosen by the
                // codec gate, never by hand.
                Button {} label: {
                    Label("Compatible version · up to 1080p", systemImage: "wand.and.rays")
                }
                .disabled(true)
            }
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
        }
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
                }
                .disabled(!model.canGoPrevious)
                .opacity(model.canGoPrevious ? 1 : 0.35)
            }
            Button { model.skip(by: -10) } label: {
                Image(systemName: "gobackward.10")
            }
            Button { model.togglePlay() } label: {
                Image(systemName: model.engine.isPlaying ? "pause.fill" : "play.fill")
                    .font(.system(size: 40, weight: .bold))
                    .frame(width: 56, height: 56)
            }
            Button { model.skip(by: 10) } label: {
                Image(systemName: "goforward.10")
            }
            if model.hasContext {
                Button {
                    Task { await model.goNext() }
                } label: {
                    Image(systemName: "forward.end.fill")
                }
                .disabled(!model.canGoNext && model.upNext.isEmpty)
                .opacity(model.canGoNext || !model.upNext.isEmpty ? 1 : 0.35)
            }
        }
        .font(.system(size: 26, weight: .semibold))
        .foregroundStyle(.white)
    }

    private var bottomBar: some View {
        VStack(spacing: 4) {
            ScrubberView(
                currentTime: model.engine.currentTime,
                duration: model.engine.duration,
                chapters: model.chapters,
                sponsors: model.video?.sponsorblock ?? [],
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
                }
                .padding(.leading, 8)
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
