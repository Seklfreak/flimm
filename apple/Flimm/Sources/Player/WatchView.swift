import FlimmKit
import SwiftUI

/// The watch screen: the player and everything under it.
///
/// The phone presents it over the tab bar so playback can start from any tab;
/// the iPad pushes it into the detail column and puts the chapter list and up
/// next beside the video instead of under it. Both read the session from
/// ``PlayerCoordinator`` rather than owning one, so resizing an iPad window —
/// which swaps the shell and rebuilds this view — never restarts playback.
struct WatchView: View {
    @Environment(PlayerCoordinator.self) private var player
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.verticalSizeClass) private var verticalSizeClass
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass

    @Environment(AppModel.self) private var app

    @State private var controlsVisible = true
    /// The picture's height, and where the bottom chrome starts within it.
    /// Both measured: a cue's lift is a proportion of the first and has to
    /// clear the second, and neither is a number this file can assume. See
    /// ``SubtitleLift``.
    @State private var stageHeight: CGFloat = 0
    @State private var chromeTop: CGFloat = 0
    @State private var scrubPreview = ScrubPreviewState()
    @State private var hideTask: Task<Void, Never>?
    @FocusState private var keyboardFocused: Bool

    /// Landscape on a phone is full-screen video; so is the explicit toggle.
    private var isFullScreen: Bool {
        player.isFullScreen || verticalSizeClass == .compact
    }

    /// The stills to load, or nil while there is nothing to load them for:
    /// no video, no preview on this backend, or playback not started yet.
    private var scrubPreviewPath: String? {
        guard let model = player.model, model.engine.currentTime > 0 else { return nil }
        return model.video?.scrubPreviewPath
    }

    private func loadScrubPreview() async {
        guard let path = scrubPreviewPath else { return }
        await scrubPreview.load(path: path, client: app.client)
    }

    /// Side-by-side only where there is room for it.
    private var isWide: Bool {
        horizontalSizeClass == .regular && !isFullScreen
    }

    var body: some View {
        Group {
            if let model = player.model {
                if isFullScreen {
                    stage(model)
                        .ignoresSafeArea()
                } else if isWide {
                    wide(model)
                } else {
                    narrow(model)
                }
            } else {
                LoadingState(label: "Loading video…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background(Palette.background)
        // Only once playback has actually begun: asking for the stills is what
        // makes the server derive them, and a video opened and abandoned in two
        // seconds should not cost a full decode. The key is nil until then, so
        // the task runs on the transition and once per video.
        .task(id: scrubPreviewPath) { await loadScrubPreview() }
        .statusBarHidden(isFullScreen)
        .navigationTitle(player.model?.video?.title ?? "")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(isFullScreen ? .hidden : .automatic, for: .navigationBar)
        .toolbar {
            // The phone presents this screen modally, which has no Back
            // button; the overlay's own chevron hides itself and is absent
            // entirely on the codec-gate and failure views.
            if horizontalSizeClass == .compact {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Close", systemImage: "xmark") { player.dismiss() }
                        .accessibilityLabel("Close player")
                }
            }
        }
        // Hardware keyboard, matching the web client's map. The focus has to be
        // somewhere for key presses to arrive, and it belongs here rather than
        // in a text field — typing in one takes focus back, which is exactly
        // why `space` never eats a search query.
        .focusable()
        .focusEffectDisabled()
        .focused($keyboardFocused)
        .onKeyPress(phases: .down) { press in handle(press) }
        .onAppear {
            keyboardFocused = true
            scheduleHide()
            Analytics.screen(.watch)
        }
        .onChange(of: scenePhase) { _, phase in
            guard phase != .active else { return }
            let model = player.model
            Task { await model?.handleBackground() }
        }
        .onDisappear { hideTask?.cancel() }
    }

    // MARK: - Layout

    /// Phone (and a narrow iPad window): video, then everything under it.
    private func narrow(_ model: WatchModel) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                stage(model)
                    .aspectRatio(16 / 9, contentMode: .fit)
                header(model)
                ChapterListView(chapters: model.chapters, activeIndex: model.activeChapter) { model.seek(to: $0) }
                CommentsSection(videoID: model.videoId)
                UpNextList(model: model, columnWidth: nil)
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 32)
        }
    }

    /// iPad: the player takes about two thirds of the width, with the chapter
    /// list and up next in their own scroller beside it.
    private func wide(_ model: WatchModel) -> some View {
        GeometryReader { geo in
            let available = max(geo.size.width - 60, 320)
            let videoWidth = max(available * 0.66, 280)
            // What's left for the side column, e.g. an iPad in portrait with
            // the sidebar showing. `UpNextList` needs this, not just
            // `horizontalSizeClass`, because its row layout has to react to
            // this specific width, not to compact-vs-regular.
            let columnWidth = max(available - videoWidth, 0)
            HStack(alignment: .top, spacing: 20) {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        stage(model)
                            .aspectRatio(16 / 9, contentMode: .fit)
                        header(model)
                        // Under the description, as on the phone and the web,
                        // rather than in the column beside the video: comments
                        // are about what is being watched, and reading them
                        // next to "up next" put them where nothing else about
                        // this video was.
                        CommentsSection(videoID: model.videoId)
                    }
                    .padding(.bottom, 24)
                }
                .frame(width: videoWidth)
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        ChapterListView(chapters: model.chapters, activeIndex: model.activeChapter) { model.seek(to: $0) }
                        UpNextList(model: model, columnWidth: columnWidth)
                    }
                    .padding(.bottom, 24)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, 20)
            .padding(.top, 8)
        }
    }

    @ViewBuilder
    private func header(_ model: WatchModel) -> some View {
        if let video = model.video {
            VideoHeader(model: model, video: video)
        }
    }

    private func stage(_ model: WatchModel) -> some View {
        // Everything but "the video is on screen": the codec wall, the
        // audio-only apology, an outright failure, and the wait while the
        // server builds a compatible rendition. None of them wants the
        // transport chrome drawn over it.
        let blocked = model.codecIssue != nil || model.audioUnavailable || model.isPreparingCompatible
        return ZStack {
            // The picture's own height, which is what the cue's lift is a
            // proportion of; and the space the chrome measures itself against.
            GeometryReader { geo in
                Color.clear
                    .onAppear { stageHeight = geo.size.height }
                    .onChange(of: geo.size.height) { _, height in stageHeight = height }
            }
            Color.black
            if model.codecIssue != nil {
                CodecGateView(model: model)
            } else if model.audioUnavailable {
                AudioUnavailableView(model: model)
            } else if let failure = model.engine.failure, !model.isPreparingCompatible {
                // While the rendition is still being made, a failed item means
                // "not ready yet" — the model is already retrying it.
                PlaybackFailureView(message: failure)
            } else if model.audioOnly {
                artwork(model)
            } else {
                PlayerSurface(engine: model.engine)
            }
            if model.isPreparingCompatible {
                PreparingCompatibleView(progress: model.compatibleProgress)
            }
            if blocked && isFullScreen {
                // No overlay controls on these views, and full screen hides
                // the toolbar — so this is the only way out in landscape.
                Button(action: close) {
                    Image(systemName: "xmark")
                        .font(.system(size: 17, weight: .semibold))
                        .foregroundStyle(.white)
                        .padding(10)
                }
                .accessibilityLabel("Close player")
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                .padding(8)
            }
            if !blocked {
                // A category the viewer set to "ask", offered for as long as
                // playback is inside it — and *not* inside `PlayerControls`,
                // which hides itself after a few seconds: a skip button you
                // have to tap the screen to see is one tap worse than useless.
                if let offer = SponsorRules.segmentToOffer(
                    at: model.engine.currentTime, in: model.video?.sponsorblock ?? [], prefs: model.prefs
                ) {
                    SkipSegmentButton(category: offer.category) { model.seek(to: offer.end) }
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
                        .padding(.trailing, 14)
                        // Above the cue, wherever the cue has ended up.
                        .padding(.bottom, cueLift + 40)
                }
                SubtitleOverlay(text: model.activeCue, size: model.prefs.subtitleSize)
                    .frame(maxHeight: .infinity, alignment: .bottom)
                    .padding(.bottom, cueLift)
                    .allowsHitTesting(false)
                PlayerControls(
                    model: model,
                    isFullScreen: isFullScreen,
                    onClose: close,
                    onToggleFullScreen: { player.isFullScreen.toggle() },
                    scrubPreview: scrubPreview,
                    isVisible: $controlsVisible
                )
                if let resumed = model.resumedFrom, controlsVisible {
                    ResumeToast(position: resumed) { Task { await model.startOver() } }
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                        .padding(.top, 52)
                        .padding(.leading, 14)
                }
            }
        }
        .coordinateSpace(name: PlayerStage.space)
        .onPreferenceChange(PlayerChromeTopKey.self) { top in chromeTop = top }
        .contentShape(Rectangle())
        .onTapGesture { toggleControls() }
        .onAppear { scheduleHide() }
    }

    /// How far the cue sits above the bottom of the picture: a share of the
    /// picture with nothing in the way, and enough to clear the controls when
    /// they are up — which, while paused, is always (see ``scheduleHide()``).
    private var cueLift: CGFloat {
        let picture = Double(stageHeight)
        guard controlsVisible else { return CGFloat(SubtitleLift.idle(pictureHeight: picture)) }
        let bar = chromeTop > 0 ? Double(stageHeight - chromeTop) : 0
        return CGFloat(SubtitleLift.overChrome(barHeight: bar, pictureHeight: picture))
    }

    private func artwork(_ model: WatchModel) -> some View {
        VStack(spacing: 14) {
            MediaImage(path: model.video?.thumbUrl, contentMode: .fit)
                .aspectRatio(16 / 9, contentMode: .fit)
                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                .padding(.horizontal, 40)
            Label("Audio only", systemImage: "headphones")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.white.opacity(0.8))
        }
    }

    // MARK: - Keyboard

    private func handle(_ press: KeyPress) -> KeyPress.Result {
        guard let model = player.model, let command = PlayerCommand.keyMap[press.key.character] else {
            return .ignored
        }
        // Escape belongs to whatever is presenting the player unless the
        // player is the thing filling the screen.
        if case .exitFullScreen = command, !player.isFullScreen { return .ignored }
        apply(command, to: model)
        showControlsBriefly()
        return .handled
    }

    private func apply(_ command: PlayerCommand, to model: WatchModel) {
        switch command {
        case .playPause: model.togglePlay()
        case .seek(let delta): model.skip(by: delta)
        case .step(let direction): Task { await direction > 0 ? model.goNext() : model.goPrevious() }
        case .chapter(let direction): model.jumpChapter(direction)
        case .speed(let direction): Task { await model.stepSpeed(direction) }
        case .fullScreen: player.isFullScreen.toggle()
        case .exitFullScreen: player.isFullScreen = false
        case .mute: model.toggleMute()
        case .subtitles: Task { await toggleSubtitles(model) }
        }
    }

    private func toggleSubtitles(_ model: WatchModel) async {
        let tracks = model.video?.subtitles ?? []
        guard let first = SubtitleLoader.pick(from: tracks, preferred: model.prefs.subtitleLang) ?? tracks.first else { return }
        let off = model.prefs.subtitleLang == Prefs.subtitlesOff
        await model.setSubtitleLanguage(off ? first.lang : Prefs.subtitlesOff)
    }

    // MARK: - Actions

    private func close() {
        if player.isFullScreen {
            player.isFullScreen = false
            return
        }
        player.dismiss()
    }

    private func toggleControls() {
        controlsVisible.toggle()
        scheduleHide()
    }

    private func showControlsBriefly() {
        controlsVisible = true
        scheduleHide()
    }

    /// Chrome fades after a few idle seconds, but never while paused — a paused
    /// player with no controls looks broken.
    private func scheduleHide() {
        hideTask?.cancel()
        guard controlsVisible else { return }
        hideTask = Task {
            try? await Task.sleep(for: .seconds(3.5))
            guard !Task.isCancelled, player.model?.engine.isPlaying == true else { return }
            controlsVisible = false
        }
    }
}

/// What a hardware key does in the player.
///
/// The map mirrors the web client's (`frontend/src/player/Player.tsx`) so the
/// two clients feel the same, with the arrows widened to ±10 s to match the
/// on-screen skip buttons and speed added on `,` / `.`. Keeping it as data
/// rather than a long switch is what makes it easy to compare the two.
enum PlayerCommand {
    case playPause
    /// Seconds, signed.
    case seek(Double)
    /// Previous/next video in the current context.
    case step(Int)
    case chapter(Int)
    case speed(Int)
    case fullScreen
    case exitFullScreen
    case mute
    case subtitles

    static let keyMap: [Character: PlayerCommand] = [
        " ": .playPause,
        "k": .playPause,
        "j": .seek(-10),
        "l": .seek(10),
        KeyEquivalent.leftArrow.character: .seek(-10),
        KeyEquivalent.rightArrow.character: .seek(10),
        KeyEquivalent.escape.character: .exitFullScreen,
        "n": .step(1),
        "p": .step(-1),
        "[": .chapter(-1),
        "]": .chapter(1),
        "f": .fullScreen,
        "m": .mute,
        ",": .speed(-1),
        ".": .speed(1),
        "c": .subtitles
    ]
}

/// The wait while the server transcodes a compatible rendition.
///
/// It is shown from the moment the archived file is ruled out until the player
/// reports `readyToPlay` — including across the retries a playlist whose job
/// failed to start (`503`, `Retry-After: 5`) provokes, which is why
/// "preparing" and not "failed" is what a viewer sees. `progress` is the
/// server's own, polled while the wait lasts.
struct PreparingCompatibleView: View {
    var progress: Double = 0

    var body: some View {
        VStack(spacing: 12) {
            ProgressView()
                .tint(.white)
                .scaleEffect(1.3)
            Text(VideoQuality.preparingTitle(progress))
                .font(.subheadline.weight(.semibold))
                .contentTransition(.numericText())
            Text("""
            This device can't decode the archived file, so the server is converting it. \
            It starts where you left off, so playback begins as soon as that part is ready.
            """)
                .font(.caption)
                .foregroundStyle(.white.opacity(0.7))
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: 340)
        }
        .foregroundStyle(.white)
        .padding(24)
    }
}

/// "This video's codec can't be played on this device."
///
/// The last resort, and only on a server that predates `hls_url`: with the
/// compatible rendition available the player uses it instead of apologising.
struct CodecGateView: View {
    let model: WatchModel

    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 30))
            Text("This video's codec (\(model.codecIssue?.videoCodec ?? "unknown")) can't be played on this device")
                .font(.subheadline.weight(.semibold))
                .multilineTextAlignment(.center)
            Text("The archive keeps whatever was downloaded, and this device has no decoder for it.")
                .font(.caption)
                .foregroundStyle(.white.opacity(0.7))
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
            if model.codecIssue?.audioAvailable == true {
                Button {
                    Task { await model.toggleAudioOnly() }
                } label: {
                    Label("Play audio only", systemImage: "headphones")
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .foregroundStyle(.white)
        .padding(24)
    }
}

/// Audio-only was requested — a music playlist, or the codec-gate fallback —
/// but the server has no `audio_aac_url` for this video. `audio_url` (Opus in
/// WebM) is never tried as a substitute; AVFoundation cannot decode it.
struct AudioUnavailableView: View {
    let model: WatchModel

    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: "headphones.slash")
                .font(.system(size: 30))
            Text("Audio-only needs a newer Flimm server")
                .font(.subheadline.weight(.semibold))
                .multilineTextAlignment(.center)
            Text("This server doesn't offer an AAC audio rendition yet, which is what AVFoundation needs to play audio only.")
                .font(.caption)
                .foregroundStyle(.white.opacity(0.7))
                .multilineTextAlignment(.center)
            // `audioUnavailable` only becomes true from an audio-only
            // request, so switching back to video is always the way out.
            Button {
                Task { await model.toggleAudioOnly() }
            } label: {
                Label("Switch to video", systemImage: "play.rectangle")
            }
            .buttonStyle(.borderedProminent)
        }
        .foregroundStyle(.white)
        .padding(24)
    }
}

struct PlaybackFailureView: View {
    let message: String

    var body: some View {
        VStack(spacing: 10) {
            Image(systemName: "play.slash")
                .font(.system(size: 28))
            Text("Playback failed")
                .font(.subheadline.weight(.semibold))
            Text(message)
                .font(.caption)
                .foregroundStyle(.white.opacity(0.7))
                .multilineTextAlignment(.center)
        }
        .foregroundStyle(.white)
        .padding(24)
    }
}
