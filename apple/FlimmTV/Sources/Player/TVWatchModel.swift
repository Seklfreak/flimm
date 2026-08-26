import AVFoundation
import AVKit
import FlimmKit
import Foundation
import Observation
import UIKit

/// One watching session on Apple TV.
///
/// It owns the `AVPlayer`; `AVPlayerViewController` only presents it. That
/// split matters because the transport bar, the chapter markers, the sponsor
/// interstitials and the skip-to-next gesture all belong to the view controller
/// while resume, watch state, context and the heartbeat belong here — and
/// every one of those answers comes from the server, not from this model.
@MainActor
@Observable
final class TVWatchModel {
    let player = AVPlayer()

    private(set) var videoId: String
    private(set) var context: PlaybackContext
    private(set) var video: Video?
    private(set) var nav: Nav?
    private(set) var chapters: [Chapter] = []
    private(set) var upNext: [VideoSummary] = []
    private(set) var cues: [SubtitleCue] = []
    private(set) var activeCue: String?
    private(set) var loadError: String?
    private(set) var codecIssue: CodecGate.Issue?
    /// Audio-only was asked for but the server has no `audio_aac_url` for this
    /// video — an older backend. `audio_url` (Opus in WebM) is never tried;
    /// AVFoundation cannot decode it.
    private(set) var audioUnavailable = false
    /// Non-nil right after a resume, so the info panel can offer "Start over".
    private(set) var resumedFrom: Double?
    private(set) var isWatched = false
    private(set) var isLoading = true
    private(set) var audioOnly: Bool
    private(set) var artwork: UIImage?
    /// SponsorBlock segments as transport-bar interstitials; the view
    /// controller reads this and nothing else about sponsors.
    private(set) var interstitials: [AVInterstitialTimeRange] = []
    /// Bumped whenever the view controller has to re-apply item-level state.
    private(set) var itemGeneration = 0

    @ObservationIgnored private let app: AppModel
    @ObservationIgnored private let client: APIClient
    @ObservationIgnored private let reporter: ProgressReporter
    @ObservationIgnored private var startAtOverride: Double?
    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var endObserver: (any NSObjectProtocol)?
    @ObservationIgnored private var lastNowPlayingUpdate: Double = -10
    @ObservationIgnored private var pendingSeek: Double?

    var prefs: Prefs { app.prefs }
    var hasContext: Bool { context.source != nil }
    var canGoNext: Bool { nav?.next != nil || !upNext.isEmpty }
    var canGoPrevious: Bool { nav?.previous != nil }
    var currentTime: Double { player.currentTime().seconds.isFinite ? player.currentTime().seconds : 0 }

    init(request: TVPlayRequest, app: AppModel) {
        self.app = app
        self.client = app.client
        self.videoId = request.videoId
        self.context = request.context
        self.audioOnly = request.context.audioOnly
        self.startAtOverride = request.startAt
        self.reporter = ProgressReporter(client: app.client)
        observeTime()
    }

    // MARK: - Loading

    func load() async {
        isLoading = true
        loadError = nil
        codecIssue = nil
        resumedFrom = nil
        activeCue = nil
        interstitials = []
        do {
            let detail = try await client.video(videoId)
            video = detail
            isWatched = detail.watched
            codecIssue = CodecGate.issue(for: detail, audioOnly: audioOnly)
            await startPlayback(detail)
            isLoading = false
            await loadSidecars(detail)
        } catch {
            loadError = AppModel.message(for: error)
            isLoading = false
        }
    }

    private func startPlayback(_ detail: Video) async {
        guard codecIssue == nil else { return }
        audioUnavailable = false
        let path: String?
        if audioOnly {
            path = detail.nativeAudioURL
            if path == nil {
                audioUnavailable = true
                return
            }
        } else {
            path = detail.mediaUrl
        }
        guard let path, !path.isEmpty, let url = client.mediaURL(path) else {
            loadError = "This video has no playable media URL."
            return
        }
        let headers = (try? await client.mediaHeaders()) ?? [:]
        // Resume is the default action: any saved position on an unwatched
        // video resumes, and only a subtitle hit overrides it.
        let resume = startAtOverride ?? (detail.watched ? 0 : detail.position)
        if startAtOverride == nil, resume > 0 { resumedFrom = resume }
        startAtOverride = nil

        TVNowPlaying.configureAudioSession()
        // `assetHTTPHeaderFieldsKey` is how the bearer token reaches /media,
        // including on the byte-range requests seeking makes.
        let asset = AVURLAsset(url: url, options: [APIClient.assetHTTPHeaderFieldsKey: headers])
        let item = AVPlayerItem(asset: asset)
        observeEnd(of: item)
        player.replaceCurrentItem(with: item)
        pendingSeek = resume > 0 ? resume : nil
        player.playImmediately(atRate: Float(prefs.playbackSpeed))
        itemGeneration += 1
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
        interstitials = TVPlayerMarkers.interstitials(for: detail.sponsorblock)
        itemGeneration += 1
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

    func seek(to seconds: Double) {
        let target = max(0, seconds)
        player.seek(to: CMTime(seconds: target, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
        resumedFrom = nil
        Task { await reporter.flush() }
    }

    /// "Start over": clear the server-side position, then rewind.
    func startOver() async {
        resumedFrom = nil
        try? await client.startOver(videoId)
        seek(to: 0)
    }

    func setSpeed(_ rate: Double) async {
        if player.timeControlStatus != .paused { player.rate = Float(rate) }
        await app.updatePrefs(PrefsPatch(playbackSpeed: rate))
        pushNowPlaying(force: true)
    }

    func setAutoplay(_ enabled: Bool) async {
        await app.updatePrefs(PrefsPatch(autoplay: enabled))
    }

    func setSkipSponsors(_ enabled: Bool) async {
        await app.updatePrefs(PrefsPatch(skipSponsors: enabled))
    }

    func setSubtitleLanguage(_ lang: String) async {
        await app.updatePrefs(PrefsPatch(subtitleLang: lang))
        guard let video else { return }
        activeCue = nil
        await loadSubtitles(video)
    }

    func setWatched(_ watched: Bool) async {
        isWatched = watched
        try? await client.setWatched(videoId, watched: watched)
        await app.refreshFeeds()
    }

    func toggleAudioOnly() async {
        audioOnly.toggle()
        context = PlaybackContext(source: context.source, shuffleSeed: context.shuffleSeed, audioOnly: audioOnly)
        guard let video else { return }
        startAtOverride = currentTime
        codecIssue = CodecGate.issue(for: video, audioOnly: audioOnly)
        await startPlayback(video)
    }

    // MARK: - Moving between videos

    /// Mapped to the remote's skip-forward gesture through
    /// `AVPlayerViewControllerSkippingBehavior.skipItem`, and to autoplay when
    /// an item ends.
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
        chapters = []
        await load()
    }

    // MARK: - Lifecycle

    func tearDown() async {
        await reporter.stop()
        if let timeObserver { player.removeTimeObserver(timeObserver) }
        timeObserver = nil
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        endObserver = nil
        player.pause()
        player.replaceCurrentItem(with: nil)
        TVNowPlaying.clear()
        TVNowPlaying.deactivateAudioSession()
    }

    // MARK: - Internals

    private func beginReporting() async {
        await reporter.onResult { [weak self] result in
            guard let model = self else { return }
            await model.applyProgress(result)
        }
        await reporter.start(videoId: videoId, context: context) { [weak self] in
            guard let model = self else { return 0 }
            return await model.currentTime
        }
    }

    private func applyProgress(_ result: ProgressResult) {
        guard result.watched, !isWatched else { return }
        isWatched = true
    }

    private func observeTime() {
        let interval = CMTime(seconds: 0.2, preferredTimescale: 600)
        timeObserver = player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { [weak self] time in
            MainActor.assumeIsolated { self?.tick(time.seconds) }
        }
    }

    private func tick(_ seconds: Double) {
        guard seconds.isFinite else { return }
        // A seek issued before the item was ready is silently dropped, so the
        // resume position is re-applied once it reports `readyToPlay`.
        if let pending = pendingSeek, player.currentItem?.status == .readyToPlay {
            pendingSeek = nil
            player.seek(to: CMTime(seconds: pending, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
            return
        }
        activeCue = WebVTT.cue(at: seconds, in: cues)?.text
        if prefs.skipSponsors, let segment = SponsorRules.segmentToSkip(at: seconds, in: video?.sponsorblock ?? []) {
            player.seek(to: CMTime(seconds: segment.end, preferredTimescale: 600))
        }
        pushNowPlaying(force: false)
    }

    private func observeEnd(of item: AVPlayerItem) {
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        endObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.didPlayToEndTimeNotification,
            object: item,
            queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                Task { await self.handleEnded() }
            }
        }
    }

    private func handleEnded() async {
        await reporter.flush()
        guard prefs.autoplay else { return }
        await goNext()
    }

    /// Only meaningful for audio-only playback; with a video on screen
    /// `AVPlayerViewController` publishes its own metadata.
    private func pushNowPlaying(force: Bool) {
        guard audioOnly else { return }
        let now = currentTime
        guard force || abs(now - lastNowPlayingUpdate) >= 2 else { return }
        lastNowPlayingUpdate = now
        guard let video else { return }
        let itemDuration = player.currentItem?.duration.seconds ?? 0
        TVNowPlaying.update(TVNowPlayingState(
            title: video.title,
            artist: video.channel.name,
            duration: itemDuration.isFinite && itemDuration > 0 ? itemDuration : video.duration,
            position: now,
            rate: player.rate == 0 ? 0 : prefs.playbackSpeed,
            artwork: artwork
        ))
    }
}
