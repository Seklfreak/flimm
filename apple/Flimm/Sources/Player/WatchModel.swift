// swiftlint:disable file_length
// A watching session is one object on purpose: everything here guards the
// same private playback state, and splitting the file means loosening the
// access that keeps that state coherent.
import FlimmKit
import Foundation
import Observation
import UIKit

/// One watching session. It survives moving between videos so the `AVPlayer`,
/// the audio session and the heartbeat are not torn down on every next/previous.
///
/// Everything that decides *what* plays comes from the server: `position` for
/// resume, `nav` for previous/next, `up-next` for autoplay, `chapters` for the
/// markers. This model wires them together and never re-derives them.
@MainActor
@Observable
final class WatchModel {
    let engine = PlayerEngine()

    private(set) var videoId: String
    private(set) var context: PlaybackContext
    private(set) var video: Video?
    private(set) var nav: Nav?
    private(set) var chapters: [Chapter] = []
    /// The backwards half of the up-next panel: what already played before
    /// this video in its context, closest first. The first page loads with
    /// the other sidecars; "Show earlier" pages further back.
    private(set) var previous: [VideoSummary] = []
    private(set) var hasMorePrevious = false
    private var previousPage = 0

    private(set) var upNext: [VideoSummary] = [] {
        // A load, a dismissal or an undo: the lock screen has to agree.
        didSet { nowPlaying.register(hasNext: canGoNext || !upNext.isEmpty, hasPrevious: canGoPrevious) }
    }
    private(set) var cues: [SubtitleCue] = []
    private(set) var activeCue: String?
    private(set) var activeChapter: Int = -1
    private(set) var loadError: String?
    /// Set only when the video has nowhere to go: a codec this device cannot
    /// decode on a server that predates `hls_url`. ``CodecGate`` decides.
    private(set) var codecIssue: CodecGate.Issue?
    /// True while the compatible H.264/AAC rendition is playing instead of the
    /// archived file.
    private(set) var usingCompatibleRendition = false
    /// The server-reported state of that rendition, refreshed on every
    /// attempt. `done` means the wait is AVFoundation's, not the transcode's.
    private(set) var compatibleState: HLSState?
    /// How far that rendition has been encoded, 0…1 — what turns the
    /// preparing overlay into "Preparing… 37%". 0 when the server has not
    /// said, which is also what a backend without the field reports.
    private(set) var compatibleProgress: Double = 0
    /// Which rung of the ladder is playing, when one is. `nil` while the
    /// archived file plays, and also on a server that offers `hls_url` without
    /// `hls_variants` — there the rendition has no height to name.
    private(set) var activeVariant: HLSVariant?
    /// Set when the rendition never became playable inside the retry window,
    /// so the failure is finally shown rather than retried forever.
    private(set) var compatibleGaveUp = false
    /// Audio-only was requested but the server has no `audio_aac_url` for
    /// this video — an older backend. `audio_url` (Opus in WebM) is never
    /// tried; AVFoundation cannot decode it.
    private(set) var audioUnavailable = false
    /// Non-nil right after a resume; the toast offers "Start over".
    private(set) var resumedFrom: Double?
    private(set) var isWatched = false
    private(set) var isLoading = true
    private(set) var audioOnly: Bool
    private(set) var lastSkippedSponsor: String?

    @ObservationIgnored private let app: AppModel
    @ObservationIgnored private let client: APIClient
    /// Per-device, unlike ``prefs``: quality belongs to this screen and this
    /// network, so it never goes to `PATCH /me/prefs`.
    @ObservationIgnored private let playback: PlaybackSettings
    @ObservationIgnored private let reporter: ProgressReporter
    @ObservationIgnored private let nowPlaying = NowPlayingController()
    @ObservationIgnored private var startAtOverride: Double?
    @ObservationIgnored private var artwork: UIImage?
    /// Mutes and unmutes for SponsorBlock `mute` segments, keeping the
    /// viewer's own mute setting intact.
    @ObservationIgnored private lazy var services = PlaybackServices(client: client, platform: "ios")
    /// When the current run of attempts at the compatible rendition began. It
    /// rolls forward while the rendition actually plays, so a mid-playback
    /// stumble gets its own window rather than inheriting a spent one.
    @ObservationIgnored private var compatibleSince: Date?
    @ObservationIgnored private var compatibleRetry: Task<Void, Never>?
    /// Keeps the transcode pointed where the viewer is, and says how far it
    /// has got. Only the compatible rendition uses it.
    @ObservationIgnored private let steering: RenditionSteering

    /// How long to keep retrying a playlist the server could not open. A
    /// segment that has not been encoded yet no longer lands here — it blocks
    /// server-side and arrives late — so this is the job failing to start at
    /// all: `503` with `Retry-After: 5`, several of them in a row.
    private static let compatibleRetryWindow: TimeInterval = 120
    private static let compatibleRetryDelay: Duration = .seconds(5)

    var prefs: Prefs { app.prefs }
    /// Reading it here keeps the menu's checkmark observing the settings
    /// object rather than a copy of its value.
    var videoQuality: QualityPreference { playback.videoQuality }
    /// What the quality menu lists, tallest first. Empty on a server without
    /// `hls_variants`, which is the signal to offer no picker at all.
    var qualityLadder: [HLSVariant] { video?.hlsLadder ?? [] }
    /// True when the archived file plays on this device — what makes
    /// ``QualityPreference/auto`` mean "the source, at full quality".
    var archivePlaysNatively: Bool {
        guard let video else { return false }
        return CodecGate.archivePlays(video, on: .current)
    }
    var hasContext: Bool { context.source != nil }
    /// "Preparing a compatible version…": the rendition is what will play, the
    /// player has nothing yet, and the server has not said it is on disk.
    var isPreparingCompatible: Bool {
        usingCompatibleRendition && !engine.isReady && compatibleState != .done && !compatibleGaveUp
    }
    var canGoNext: Bool { nav?.next != nil }
    var canGoPrevious: Bool { nav?.previous != nil }

    init(request: PlayRequest, app: AppModel, playback: PlaybackSettings) {
        self.app = app
        self.client = app.client
        self.playback = playback
        self.videoId = request.videoId
        self.context = request.context
        self.audioOnly = request.context.audioOnly
        self.startAtOverride = request.startAt
        self.reporter = ProgressReporter(client: app.client)
        self.steering = RenditionSteering(client: app.client)
        wireSteering()
        wireEngine()
        wireRemoteCommands()
    }

    // MARK: - Loading

    func load() async {
        isLoading = true
        loadError = nil
        codecIssue = nil
        audioUnavailable = false
        resumedFrom = nil
        activeCue = nil
        activeChapter = -1
        compatibleSince = nil
        compatibleGaveUp = false
        compatibleState = nil
        compatibleProgress = 0
        do {
            let detail = try await client.video(videoId)
            video = detail
            isWatched = detail.watched
            await startPlayback(detail)
            isLoading = false
            await loadSidecars(detail)
        } catch {
            loadError = AppModel.message(for: error)
            isLoading = false
        }
    }

    /// Picks the stream and opens it.
    ///
    /// Three paths, in the order they cost the server: the archived file when
    /// this device can decode it, the derived AAC audio when audio-only was
    /// asked for, and the compatible H.264/AAC rendition when neither. The
    /// last is a real transcode of someone's CPU, which is why ``CodecGate``
    /// is the only thing allowed to choose it.
    private func startPlayback(_ detail: Video) async {
        services.startingVideo()
        compatibleRetry?.cancel()
        compatibleRetry = nil
        steering.cancel()
        audioUnavailable = false
        codecIssue = nil
        usingCompatibleRendition = false
        activeVariant = nil

        // Resume is the default action, and it is settled before anything is
        // started: the server encodes from here first, which is what makes
        // resuming an hour in immediate.
        let resume = startAtOverride ?? (detail.watched ? 0 : detail.position)
        let isResume = startAtOverride == nil && resume > 0
        startAtOverride = nil

        let path: String
        // For an HLS rendition the player URL carries `?from=<resume>`, so the
        // media playlist comes back with `#EXT-X-START` and AVPlayer begins at
        // the resume point (or, on a quality switch, the clock the viewer was
        // at) — fetching that segment first instead of blocking on segment 0.
        var mediaFrom: Int?
        if audioOnly {
            guard let nativeAudioURL = detail.nativeAudioURL else {
                // Audio-only was requested but this server predates
                // `audio_aac_url`. `audio_url` (Opus in WebM) is never a
                // valid fallback — AVFoundation cannot decode it.
                audioUnavailable = true
                return
            }
            path = nativeAudioURL
        } else {
            switch CodecGate.decision(for: detail, preference: playback.videoQuality, device: .current) {
            case .native:
                path = detail.mediaUrl
            case .hls(let choice):
                path = choice.url
                mediaFrom = resume > 0 ? Int(resume.rounded(.down)) : nil
                usingCompatibleRendition = true
                activeVariant = choice.variant
                compatibleSince = compatibleSince ?? Date()
                // Start the job before AVFoundation opens the playlist, with
                // `from` so it encodes the part about to be played first. The
                // call is idempotent and reports where the rendition stands.
                // Only the height about to play is started: the server
                // transcodes one at a time.
                let status = await steering.start(videoId: videoId, height: choice.height, from: resume)
                compatibleState = status?.state ?? choice.state ?? detail.hlsState
                compatibleProgress = status?.progress ?? choice.variant?.progress ?? 0
                steering.adopt(state: compatibleState)
            case .audioOnly(let issue), .unplayable(let issue):
                codecIssue = issue
                return
            }
        }
        guard !path.isEmpty, let url = client.mediaURL(path, from: mediaFrom) else {
            loadError = "This video has no playable media URL."
            return
        }
        let headers = (try? await client.mediaHeaders()) ?? [:]
        if isResume { resumedFrom = resume }

        NowPlayingController.configureAudioSession()
        engine.load(
            url: url,
            headers: headers,
            startAt: resume,
            rate: prefs.playbackSpeed,
            duration: detail.duration
        )
        Analytics.play(videoID: detail.id, kind: detail.type.rawValue, audioOnly: audioOnly)
        if audioOnly { engine.detachPiP() }
        if usingCompatibleRendition {
            // "Waiting" is either overlay: nothing on screen yet, or a stall
            // on a segment the encoder has not reached.
            steering.poll { [weak self] in
                guard let self else { return false }
                return !self.engine.isReady || self.engine.isBuffering
            }
        }
        await beginReporting()
    }

    /// A playlist the server cannot open — the job failed to start — answers
    /// `503` with `Retry-After: 5`, which fails the item outright, because
    /// `AVPlayer` has no notion of coming back later. A segment that has not
    /// been encoded yet does *not* land here: it blocks on the server and the
    /// player simply buffers. So this is still preparing, not an error, and it
    /// is retried on the server's own cadence until the window runs out.
    private func handleEngineFailure() {
        guard usingCompatibleRendition, !compatibleGaveUp, let detail = video else { return }
        guard let since = compatibleSince, Date().timeIntervalSince(since) < Self.compatibleRetryWindow else {
            compatibleGaveUp = true
            return
        }
        compatibleRetry?.cancel()
        compatibleRetry = Task { [weak self] in
            try? await Task.sleep(for: Self.compatibleRetryDelay)
            guard !Task.isCancelled, let self else { return }
            // Pick up where the failed attempt was pointed, not at the
            // server-held position, which a seek may have moved past.
            self.startAtOverride = self.engine.currentTime
            await self.startPlayback(detail)
        }
    }

    private func loadSidecars(_ detail: Video) async {
        async let chapterList = fetchChapters()
        async let navigation = fetchNav()
        async let next = fetchUpNext()
        async let prev = fetchPrevious(page: 0)
        let (loadedChapters, loadedNav, loadedNext, loadedPrev) = await (chapterList, navigation, next, prev)
        chapters = loadedChapters
        nav = loadedNav
        upNext = loadedNext
        previous = loadedPrev?.items ?? []
        hasMorePrevious = loadedPrev?.hasMore ?? false
        previousPage = 1
        await loadSubtitles(detail)
        await loadArtwork(detail)
    }

    private func fetchChapters() async -> [Chapter] {
        (try? await client.chapters(videoId))?.chapters ?? []
    }

    /// Without a context there is no list to step through, so the player hides
    /// the previous/next controls rather than guessing at neighbours.
    private func fetchNav() async -> Nav? {
        guard hasContext else { return nil }
        return try? await client.nav(videoId, context: context)
    }

    private func fetchUpNext() async -> [VideoSummary] {
        (try? await client.upNext(videoId, context: context))?.items ?? []
    }

    private func loadSubtitles(_ detail: Video) async {
        guard let track = SubtitleLoader.pick(from: detail.subtitles, preferred: prefs.subtitleLang) else {
            cues = []
            return
        }
        cues = await SubtitleLoader.load(track: track, client: client)
    }

    private func loadArtwork(_ detail: Video) async {
        artwork = await MediaImageStore.shared.image(at: detail.thumbUrl, client: client)
        pushNowPlaying(force: true)
    }

    // MARK: - Transport

    func togglePlay() {
        if engine.isPlaying { pause() } else { resume() }
    }

    func resume() {
        NowPlayingController.configureAudioSession()
        engine.play()
        Task { await reporter.resume() }
        pushNowPlaying(force: true)
    }

    func pause() {
        engine.pause()
        Task { await reporter.pause() }
        pushNowPlaying(force: true)
    }

    func seek(to seconds: Double) {
        engine.seek(to: seconds)
        steering.steer(to: seconds)
        resumedFrom = nil
        Task { await reporter.flush() }
        pushNowPlaying(force: true)
    }

    func skip(by delta: Double) {
        seek(to: engine.currentTime + delta)
    }

    func toggleMute() {
        engine.toggleMute()
    }

    /// `[` and `]`. The maths is `FlimmKit`'s, shared with the web client, so
    /// "back to the start of this chapter" behaves identically in both.
    func jumpChapter(_ direction: Int) {
        let time = engine.currentTime
        let target = direction < 0
            ? ChapterMath.previousStart(before: time, in: chapters)
            : ChapterMath.nextStart(after: time, in: chapters)
        guard let target else { return }
        seek(to: target)
    }

    /// `,` and `.` — one step along the same speed list the menu offers.
    func stepSpeed(_ direction: Int) async {
        let speeds = PlaybackSpeeds.all
        let current = speeds.firstIndex(of: prefs.playbackSpeed) ?? speeds.firstIndex(of: 1.0) ?? 0
        let next = min(max(current + direction, 0), speeds.count - 1)
        guard next != current else { return }
        await setSpeed(speeds[next])
    }

    /// The list after "Not interested" took a video out or an undo put one
    /// back: up next never holds a dismissed video (`docs/api.md`), and the
    /// caller owns the undo, and with it the position to put it back at.
    func setUpNext(_ videos: [VideoSummary]) { upNext = videos }

    /// "Start over": clear the server-side position, then rewind.
    func startOver() async {
        resumedFrom = nil
        try? await client.startOver(videoId)
        engine.seek(to: 0)
        steering.steer(to: 0)
    }

    func setSpeed(_ rate: Double) async {
        engine.setRate(rate)
        await app.updatePrefs(PrefsPatch(playbackSpeed: rate))
        pushNowPlaying(force: true)
    }

    func setAutoplay(_ enabled: Bool) async {
        await app.updatePrefs(PrefsPatch(autoplay: enabled))
    }

    func setSubtitleLanguage(_ lang: String) async {
        await app.updatePrefs(PrefsPatch(subtitleLang: lang))
        guard let video else { return }
        activeCue = nil
        await loadSubtitles(video)
    }

    /// Switches rendition without leaving the video.
    ///
    /// These are independent playlists rather than one master with several
    /// `EXT-X-STREAM-INF` renditions, so a switch is a reload: remember the
    /// clock, start the new height's job, swap the item, seek back and keep
    /// playing. A rendition the server has not made yet puts the
    /// "Preparing a compatible version…" overlay up exactly as a fresh video
    /// does. The choice is per device and outlives this video.
    func setVideoQuality(_ preference: QualityPreference) async {
        guard playback.videoQuality != preference else { return }
        playback.videoQuality = preference
        guard let video, !audioOnly else { return }
        startAtOverride = engine.currentTime
        compatibleSince = nil
        compatibleGaveUp = false
        await startPlayback(video)
    }

    func toggleAudioOnly() async {
        audioOnly.toggle()
        compatibleSince = nil
        compatibleGaveUp = false
        context = PlaybackContext(source: context.source, shuffleSeed: context.shuffleSeed, audioOnly: audioOnly)
        guard let video else { return }
        startAtOverride = engine.currentTime
        await startPlayback(video)
    }

    func setWatched(_ watched: Bool) async {
        isWatched = watched
        try? await client.setWatched(videoId, watched: watched)
        await app.videoListStateChanged()
    }

    // MARK: - Moving between videos

    func goNext() async {
        if let next = nav?.next {
            await go(to: next.id)
        } else if let next = upNext.first {
            await go(to: next.id)
        }
    }

    func goPrevious() async {
        guard let previous = nav?.previous else { return }
        await go(to: previous.id)
    }

    /// A new seed is a new shuffle; the run restarts at `nav.first` so the
    /// client never derives the order itself.
    func reshuffle() async {
        let seeded = PlaybackContext(
            source: context.source,
            shuffleSeed: PlaybackContext.newShuffleSeed(),
            audioOnly: audioOnly
        )
        context = seeded
        guard let navigation = try? await client.nav(videoId, context: seeded) else { return }
        await go(to: navigation.first?.id ?? videoId)
    }

    func go(to id: String) async {
        await reporter.stop()
        videoId = id
        video = nil
        cues = []
        await load()
    }

    // MARK: - Lifecycle

    func flush() async {
        await reporter.flush()
    }

    func handleBackground() async {
        await reporter.flush()
        // Video keeps the screen; audio-only is what earns background playback.
        if !audioOnly && !engine.isPiPActive { pause() }
    }

    func tearDown() async {
        compatibleRetry?.cancel()
        compatibleRetry = nil
        services.stop()
        steering.cancel()
        await reporter.stop()
        nowPlaying.unregister()
        engine.tearDown()
        NowPlayingController.deactivateAudioSession()
    }

    private func beginReporting() async {
        // Both closures run off the main actor; binding `self` to a local
        // immutable keeps them out of Swift 6's captured-var diagnostic, and
        // hopping back is implicit because the model is `@MainActor`.
        await reporter.onResult { [weak self] result in
            guard let model = self else { return }
            await model.applyProgress(result)
        }
        await reporter.start(videoId: videoId, context: context) { [weak self] in
            guard let model = self else { return 0 }
            return await model.playbackPosition
        }
    }

    /// What the heartbeat reports: the player's own clock, which the engine
    /// holds at the position playback is about to start from until the item is
    /// ready to report a real one.
    private var playbackPosition: Double { engine.currentTime }

    private func applyProgress(_ result: ProgressResult) async {
        guard result.watched, !isWatched else { return }
        isWatched = true
        // The explicit "Mark seen" isn't the only way a video finishes —
        // playback reaching the end reports `watched` right here, and an
        // "Unseen" feed/channel list needs the same cache invalidation or it
        // keeps listing this video after the viewer goes back to it.
        await app.videoListStateChanged()
    }

    private func handleEnded() async {
        await reporter.flush()
        guard prefs.autoplay else { return }
        await goNext()
    }

    private func handleTick(_ time: Double) {
        // The retry window measures "how long without playback", so it rolls
        // forward while the rendition is actually playing.
        if usingCompatibleRendition, engine.isReady { compatibleSince = Date() }
        // The "Resumed from …" offer retires itself once it has been on
        // screen for a minute of playback; see ``ResumeNotice``.
        resumedFrom = ResumeNotice.retained(resumedFrom, currentTime: time)
        activeCue = WebVTT.cue(at: time, in: cues)?.text
        activeChapter = ChapterMath.index(of: time, in: chapters)
        // Loudness, stall reporting and SponsorBlock: three rules the TV has to
        // follow identically, so all three live in PlaybackServices.
        let sponsor = services.tick(
            .init(
                videoID: videoId,
                time: time,
                isStalled: engine.isBuffering && engine.isPlaying,
                height: activeVariant?.height ?? 0,
                segments: video?.sponsorblock ?? [],
                prefs: prefs,
                isMuted: engine.isMuted
            ),
            setGain: { [weak self] gain in self?.engine.setGain(dB: gain) }
        )
        if let to = sponsor.skipTo { engine.seek(to: to) }
        if let muted = sponsor.muted { engine.setMuted(muted) }
        if let label = sponsor.skippedLabel { lastSkippedSponsor = label }
        pushNowPlaying(force: false)
    }

    private func pushNowPlaying(force: Bool) {
        guard let video else { return }
        let now = engine.currentTime
        nowPlaying.update(NowPlayingState(
            title: video.title,
            artist: video.channel.name,
            duration: engine.duration > 0 ? engine.duration : video.duration,
            position: now,
            rate: engine.isPlaying ? prefs.playbackSpeed : 0,
            artwork: artwork
        ), force: force)
    }
}

// MARK: - Previous paging

/// The backwards half of the panel. In an extension for the same reason the
/// wiring below is: the class body stays about what a watching session *is*.
extension WatchModel {
    private func fetchPrevious(page: Int) async -> Page<VideoSummary>? {
        guard hasContext else { return nil }
        return try? await client.upNext(videoId, context: context, before: true, page: page)
    }

    /// Pages further back once "Show earlier" outruns what is loaded.
    func loadMorePrevious() async {
        guard hasMorePrevious else { return }
        hasMorePrevious = false
        guard let page = await fetchPrevious(page: previousPage) else { return }
        previous += page.items
        hasMorePrevious = page.hasMore
        previousPage += 1
    }
}

// MARK: - Wiring

/// The wiring done once in `init`: the steering callbacks, the engine's
/// callbacks and the remote-control commands. In an extension so the class
/// body stays about what a watching session *is* rather than how it is
/// hooked up.
private extension WatchModel {
    /// Everything the server says about the job while it is steered or polled
    /// lands here, so the overlay's percentage has one source.
    func wireSteering() {
        steering.onStatus = { [weak self] status in
            self?.compatibleState = status.state
            self?.compatibleProgress = status.progress
        }
    }

    func wireEngine() {
        engine.onEnded = { [weak self] in
            guard let self else { return }
            Task { await self.handleEnded() }
        }
        engine.onTick = { [weak self] time in
            self?.handleTick(time)
        }
        engine.onFailed = { [weak self] _ in
            self?.handleEngineFailure()
        }
    }

    func wireRemoteCommands() {
        nowPlaying.isPlaying = { [weak self] in self?.engine.isPlaying ?? false }
        nowPlaying.onPlay = { [weak self] in self?.resume() }
        nowPlaying.onPause = { [weak self] in self?.pause() }
        nowPlaying.onNext = { [weak self] in Task { await self?.goNext() } }
        nowPlaying.onPrevious = { [weak self] in Task { await self?.goPrevious() } }
        nowPlaying.onSeek = { [weak self] seconds in self?.seek(to: seconds) }
    }
}
