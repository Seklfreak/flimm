import FlimmKit
import Foundation
import Observation
import UIKit

/// Why a video cannot be played natively.
struct CodecIssue: Sendable, Hashable {
    let videoCodec: String
    /// Audio-only is still an option when the server offers a native audio
    /// rendition (`Video.nativeAudioURL`) for this video — absent on a
    /// backend that predates `audio_aac_url`, regardless of the archived
    /// audio codec.
    let audioAvailable: Bool
}

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
    private(set) var codecIssue: CodecIssue?
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

    var prefs: Prefs { app.prefs }
    var hasContext: Bool { context.source != nil }
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
        do {
            let detail = try await client.video(videoId)
            video = detail
            isWatched = detail.watched
            applyCodecGate(detail)
            await startPlayback(detail)
            isLoading = false
            await loadSidecars(detail)
        } catch {
            loadError = AppModel.message(for: error)
            isLoading = false
        }
    }

    /// `streams` mirrors what was downloaded. VP9/AV1 video or Opus audio is
    /// device-dependent, so a clear message beats a spinner that never resolves.
    private func applyCodecGate(_ detail: Video) {
        guard let streams = detail.streams, !streams.isEmpty else { return }
        let videoStreams = streams.filter { $0.type == .video }
        guard !videoStreams.isEmpty, !videoStreams.contains(where: DeviceCodecs.canDecode) else { return }
        codecIssue = CodecIssue(videoCodec: videoStreams[0].codec, audioAvailable: detail.nativeAudioURL != nil)
        // Audio-only sidesteps a video codec this device cannot decode.
        if audioOnly { codecIssue = nil }
    }

    private func startPlayback(_ detail: Video) async {
        guard codecIssue == nil else { return }
        audioUnavailable = false
        let path: String
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
            path = detail.mediaUrl
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
        engine.load(url: url, headers: headers, startAt: resume, rate: prefs.playbackSpeed, duration: detail.duration)
        if audioOnly { engine.detachPiP() }
        await beginReporting()
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

    private var playbackPosition: Double { engine.currentTime }

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
