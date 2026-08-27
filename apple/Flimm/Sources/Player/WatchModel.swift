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
    private(set) var upNext: [VideoSummary] = []
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
    @ObservationIgnored private let reporter: ProgressReporter
    @ObservationIgnored private let nowPlaying = NowPlayingController()
    @ObservationIgnored private var startAtOverride: Double?
    @ObservationIgnored private var artwork: UIImage?
    @ObservationIgnored private var lastNowPlayingUpdate: Double = -10
    /// When the current run of attempts at the compatible rendition began. It
    /// rolls forward while the rendition actually plays, so a mid-playback
    /// stumble gets its own window rather than inheriting a spent one.
    @ObservationIgnored private var compatibleSince: Date?
    @ObservationIgnored private var compatibleRetry: Task<Void, Never>?

    /// How long to keep retrying a rendition the server has not produced yet.
    /// The playlist gives up waiting after 45 s and answers `503` with
    /// `Retry-After: 5`, so this is several of those in a row.
    private static let compatibleRetryWindow: TimeInterval = 120
    private static let compatibleRetryDelay: Duration = .seconds(5)

    var prefs: Prefs { app.prefs }
    var hasContext: Bool { context.source != nil }
    /// "Preparing a compatible version…": the rendition is what will play, the
    /// player has nothing yet, and the server has not said it is on disk.
    var isPreparingCompatible: Bool {
        usingCompatibleRendition && !engine.isReady && compatibleState != .done && !compatibleGaveUp
    }
    var canGoNext: Bool { nav?.next != nil }
    var canGoPrevious: Bool { nav?.previous != nil }

    init(request: PlayRequest, app: AppModel) {
        self.app = app
        self.client = app.client
        self.videoId = request.videoId
        self.context = request.context
        self.audioOnly = request.context.audioOnly
        self.startAtOverride = request.startAt
        self.reporter = ProgressReporter(client: app.client)
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
        compatibleRetry?.cancel()
        compatibleRetry = nil
        audioUnavailable = false
        codecIssue = nil
        usingCompatibleRendition = false

        let path: String
        // A growing EVENT playlist reports only what has been transcoded so
        // far, so the archived duration stays authoritative for the scrubber.
        var trustsItemDuration = true
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
            switch CodecGate.decision(for: detail) {
            case .native:
                path = detail.mediaUrl
            case .hls(let compatible):
                path = compatible
                trustsItemDuration = false
                usingCompatibleRendition = true
                compatibleSince = compatibleSince ?? Date()
                // Start the job before AVFoundation opens the playlist, so the
                // transcode's head start is the server's rather than ours. The
                // call is idempotent and reports where the rendition stands.
                compatibleState = (try? await client.startHLS(videoId)) ?? detail.hlsState
            case .audioOnly(let issue), .unplayable(let issue):
                codecIssue = issue
                return
            }
        }
        guard !path.isEmpty, let url = client.mediaURL(path) else {
            loadError = "This video has no playable media URL."
            return
        }
        let headers = (try? await client.mediaHeaders()) ?? [:]
        // Resume is the default action: any saved position on an unwatched
        // video resumes, and only a subtitle hit overrides it.
        let resume = startAtOverride ?? (detail.watched ? 0 : detail.position)
        if startAtOverride == nil, resume > 0 { resumedFrom = resume }
        startAtOverride = nil

        NowPlayingController.configureAudioSession()
        engine.load(
            url: url,
            headers: headers,
            startAt: resume,
            rate: prefs.playbackSpeed,
            duration: detail.duration,
            trustsItemDuration: trustsItemDuration
        )
        if audioOnly { engine.detachPiP() }
        await beginReporting()
    }

    /// A rendition the transcode has not reached yet fails the item outright:
    /// the playlist answers `503` with `Retry-After: 5` until the first
    /// segment exists, and `AVPlayer` has no notion of "come back later". That
    /// is still preparing, not an error, so it is retried on the server's own
    /// cadence until the window runs out.
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
        let (loadedChapters, loadedNav, loadedNext) = await (chapterList, navigation, next)
        chapters = loadedChapters
        nav = loadedNav
        upNext = loadedNext
        nowPlaying.register(hasNext: canGoNext || !upNext.isEmpty, hasPrevious: canGoPrevious)
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

    /// "Start over": clear the server-side position, then rewind.
    func startOver() async {
        resumedFrom = nil
        try? await client.startOver(videoId)
        engine.seek(to: 0)
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
        await app.refreshFeeds()
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
        await reporter.stop()
        nowPlaying.unregister()
        engine.tearDown()
        NowPlayingController.deactivateAudioSession()
    }

    // MARK: - Wiring

    private func wireEngine() {
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

    private func wireRemoteCommands() {
        nowPlaying.isPlaying = { [weak self] in self?.engine.isPlaying ?? false }
        nowPlaying.onPlay = { [weak self] in self?.resume() }
        nowPlaying.onPause = { [weak self] in self?.pause() }
        nowPlaying.onNext = { [weak self] in Task { await self?.goNext() } }
        nowPlaying.onPrevious = { [weak self] in Task { await self?.goPrevious() } }
        nowPlaying.onSeek = { [weak self] seconds in self?.seek(to: seconds) }
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

    /// What the heartbeat reports.
    ///
    /// While a resume has not landed — the compatible rendition's playlist has
    /// not grown that far yet — the player is temporarily earlier in the video
    /// than the viewer is. Reporting the clock there would overwrite a good
    /// server-held position with a worse one, so the position being sought is
    /// what goes back: the same value the server already holds, which the
    /// reporter then stops repeating.
    private var playbackPosition: Double { engine.unreachedStart ?? engine.currentTime }

    private func applyProgress(_ result: ProgressResult) {
        guard result.watched, !isWatched else { return }
        isWatched = true
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
        activeCue = WebVTT.cue(at: time, in: cues)?.text
        activeChapter = ChapterMath.index(of: time, in: chapters)
        if prefs.skipSponsors, let segment = SponsorRules.segmentToSkip(at: time, in: video?.sponsorblock ?? []) {
            lastSkippedSponsor = SponsorRules.label(segment.category)
            engine.seek(to: segment.end)
        }
        pushNowPlaying(force: false)
    }

    private func pushNowPlaying(force: Bool) {
        let now = engine.currentTime
        guard force || abs(now - lastNowPlayingUpdate) >= 2 else { return }
        lastNowPlayingUpdate = now
        guard let video else { return }
        nowPlaying.update(NowPlayingState(
            title: video.title,
            artist: video.channel.name,
            duration: engine.duration > 0 ? engine.duration : video.duration,
            position: now,
            rate: engine.isPlaying ? prefs.playbackSpeed : 0,
            artwork: artwork
        ))
    }
}
