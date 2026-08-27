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
    /// Set only when the video has nowhere to go: a codec this device cannot
    /// decode on a server that predates `hls_url`. ``CodecGate`` decides.
    private(set) var codecIssue: CodecGate.Issue?
    /// True while the compatible H.264/AAC rendition is playing instead of the
    /// archived file.
    private(set) var usingCompatibleRendition = false
    /// The server-reported state of that rendition, refreshed on every attempt.
    private(set) var compatibleState: HLSState?
    /// Which rung of the ladder is playing, when one is. `nil` while the
    /// archived file plays, and on a server that offers `hls_url` without
    /// `hls_variants` — there the rendition has no height to name.
    private(set) var activeVariant: HLSVariant?
    /// Set when the rendition never became playable inside the retry window.
    private(set) var compatibleGaveUp = false
    /// True once the current item reports `readyToPlay`.
    private(set) var isReady = false
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
    /// Per-device, unlike ``prefs``: a TV on ethernet wants a different answer
    /// from a phone on cellular, so quality never goes to `PATCH /me/prefs`.
    @ObservationIgnored private let playback: PlaybackSettings
    @ObservationIgnored private let reporter: ProgressReporter
    @ObservationIgnored private var startAtOverride: Double?
    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var endObserver: (any NSObjectProtocol)?
    @ObservationIgnored private var lastNowPlayingUpdate: Double = -10
    @ObservationIgnored private var pendingSeek: Double?
    @ObservationIgnored private var statusObservation: NSKeyValueObservation?
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
    /// Read through the settings object so the Info panel's row tracks it.
    var videoQuality: QualityPreference { playback.videoQuality }
    /// What the quality picker offers, tallest first. Empty on a server
    /// without `hls_variants`, which is the signal to offer no picker.
    var qualityLadder: [HLSVariant] { video?.hlsLadder ?? [] }
    /// True when the archived file plays on this Apple TV — what makes
    /// ``QualityPreference/auto`` mean "the source, at full quality".
    var archivePlaysNatively: Bool {
        guard let video else { return false }
        return CodecGate.archivePlays(video)
    }
    var hasContext: Bool { context.source != nil }
    var canGoNext: Bool { nav?.next != nil || !upNext.isEmpty }
    var canGoPrevious: Bool { nav?.previous != nil }
    var currentTime: Double { player.currentTime().seconds.isFinite ? player.currentTime().seconds : 0 }
    /// What the heartbeat reports.
    ///
    /// While a resume has not landed — the compatible rendition's playlist has
    /// not grown that far yet — the player is temporarily earlier in the video
    /// than the viewer is. Reporting the clock there would overwrite a good
    /// server-held position with a worse one, so the position being sought is
    /// what goes back: the same value the server already holds, which the
    /// reporter then stops repeating.
    var reportedPosition: Double { pendingSeek ?? currentTime }
    /// "Preparing a compatible version…": the rendition is what will play, the
    /// player has nothing yet, and the server has not said it is on disk.
    var isPreparingCompatible: Bool {
        usingCompatibleRendition && !isReady && compatibleState != .done && !compatibleGaveUp
    }

    init(request: TVPlayRequest, app: AppModel, playback: PlaybackSettings) {
        self.app = app
        self.client = app.client
        self.playback = playback
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
    /// asked for, and the compatible H.264/AAC rendition when neither — a real
    /// transcode, which is why ``CodecGate`` is the only thing allowed to
    /// choose it. `AVPlayerViewController` plays HLS natively, so nothing else
    /// about the screen changes.
    private func startPlayback(_ detail: Video) async {
        compatibleRetry?.cancel()
        compatibleRetry = nil
        audioUnavailable = false
        codecIssue = nil
        usingCompatibleRendition = false
        activeVariant = nil
        isReady = false

        let path: String
        if audioOnly {
            guard let nativeAudioURL = detail.nativeAudioURL else {
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
                usingCompatibleRendition = true
                activeVariant = choice.variant
                compatibleSince = compatibleSince ?? Date()
                // Start the job before AVFoundation opens the playlist, so the
                // transcode's head start is the server's rather than ours. The
                // call is idempotent and reports where the rendition stands.
                // Only the height about to play is started: the server runs one
                // transcode at a time.
                compatibleState = (try? await client.startHLS(videoId, height: choice.height))
                    ?? choice.state
                    ?? detail.hlsState
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

        TVNowPlaying.configureAudioSession()
        // `assetHTTPHeaderFieldsKey` is how the bearer token reaches /media,
        // including on the byte-range requests seeking makes.
        // AVFoundation re-sends these on every request the asset makes,
        // byte-range and HLS segment alike.
        let asset = AVURLAsset(url: url, options: [APIClient.assetHTTPHeaderFieldsKey: headers])
        let item = AVPlayerItem(asset: asset)
        observeStatus(of: item)
        observeEnd(of: item)
        player.replaceCurrentItem(with: item)
        pendingSeek = resume > 0 ? resume : nil
        player.playImmediately(atRate: Float(prefs.playbackSpeed))
        itemGeneration += 1
        await beginReporting()
    }

    /// A rendition the transcode has not reached yet fails the item outright:
    /// the playlist answers `503` with `Retry-After: 5` until the first
    /// segment exists, and `AVPlayer` has no notion of "come back later". That
    /// is still preparing, not an error, so it is retried on the server's own
    /// cadence until the window runs out.
    private func handleItemFailure() {
        guard usingCompatibleRendition, !compatibleGaveUp, let detail = video else { return }
        guard let since = compatibleSince, Date().timeIntervalSince(since) < Self.compatibleRetryWindow else {
            compatibleGaveUp = true
            loadError = "Couldn't prepare a compatible version of this video."
            return
        }
        compatibleRetry?.cancel()
        compatibleRetry = Task { [weak self] in
            try? await Task.sleep(for: Self.compatibleRetryDelay)
            guard !Task.isCancelled, let self else { return }
            // Pick up where the failed attempt was pointed, not at the
            // server-held position, which a seek may have moved past. A failed
            // item's clock reads 0, so the seek it never got to is the truth.
            self.startAtOverride = self.pendingSeek ?? self.currentTime
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
        // An explicit seek replaces a resume that has not landed yet.
        pendingSeek = nil
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

    /// Switches rendition without leaving the video.
    ///
    /// The renditions are independent playlists, so a switch is a reload:
    /// remember the clock, start the new height's job, swap the item and seek
    /// back. A height the server has not made yet raises the "Preparing a
    /// compatible version…" overlay exactly as a fresh video does. The choice
    /// belongs to this Apple TV and outlives the video.
    func setVideoQuality(_ preference: QualityPreference) async {
        guard playback.videoQuality != preference else { return }
        playback.videoQuality = preference
        guard let video, !audioOnly else { return }
        startAtOverride = pendingSeek ?? currentTime
        compatibleSince = nil
        compatibleGaveUp = false
        await startPlayback(video)
    }

    func toggleAudioOnly() async {
        audioOnly.toggle()
        context = PlaybackContext(source: context.source, shuffleSeed: context.shuffleSeed, audioOnly: audioOnly)
        guard let video else { return }
        startAtOverride = currentTime
        compatibleSince = nil
        compatibleGaveUp = false
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
        compatibleRetry?.cancel()
        compatibleRetry = nil
        await reporter.stop()
        if let timeObserver { player.removeTimeObserver(timeObserver) }
        timeObserver = nil
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        endObserver = nil
        statusObservation?.invalidate()
        statusObservation = nil
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
            return await model.reportedPosition
        }
    }

    private func applyProgress(_ result: ProgressResult) {
        guard result.watched, !isWatched else { return }
        isWatched = true
    }

    /// Whether the item can actually be seeked to `target` right now. An empty
    /// `seekableTimeRanges` means the item has not said — a progressive file
    /// before its first range is published — so the seek is tried anyway.
    private static func canSeek(to target: Double, in item: AVPlayerItem) -> Bool {
        let ranges = item.seekableTimeRanges.map(\.timeRangeValue).filter { $0.duration.seconds.isFinite }
        guard !ranges.isEmpty else { return true }
        return ranges.contains { target >= $0.start.seconds - 0.5 && target <= $0.end.seconds + 0.5 }
    }

    private func observeTime() {
        let interval = CMTime(seconds: 0.2, preferredTimescale: 600)
        timeObserver = player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { [weak self] time in
            MainActor.assumeIsolated { self?.tick(time.seconds) }
        }
    }

    private func tick(_ seconds: Double) {
        guard seconds.isFinite else { return }
        // The retry window measures "how long without playback", so it rolls
        // forward while the rendition is actually playing.
        if usingCompatibleRendition, isReady { compatibleSince = Date() }
        // A seek issued before the item was ready is silently dropped, so the
        // resume position is re-applied once it reports `readyToPlay` — and,
        // on a growing HLS playlist, re-tried until the transcode has produced
        // that far, since seeking past what exists is clamped, not honoured.
        if let pending = pendingSeek, let item = player.currentItem, item.status == .readyToPlay {
            if TVWatchModel.canSeek(to: pending, in: item) {
                pendingSeek = nil
                player.seek(to: CMTime(seconds: pending, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
                return
            }
            // Playing on has reached it by itself; nothing left to honour.
            if seconds >= pending { pendingSeek = nil }
        }
        activeCue = WebVTT.cue(at: seconds, in: cues)?.text
        if prefs.skipSponsors, let segment = SponsorRules.segmentToSkip(at: seconds, in: video?.sponsorblock ?? []) {
            player.seek(to: CMTime(seconds: segment.end, preferredTimescale: 600))
        }
        pushNowPlaying(force: false)
    }

    /// `readyToPlay` is what ends a "preparing…" state, and `.failed` is the
    /// only signal a playlist the server has not finished yet produces — such
    /// an item never reaches `failedToPlayToEndTime`, because it never
    /// started. (A `503` surfaces as `NSURLErrorDomain -1008` wrapping
    /// `CoreMediaErrorDomain -16849`.)
    private func observeStatus(of item: AVPlayerItem) {
        statusObservation?.invalidate()
        statusObservation = item.observe(\.status, options: [.initial, .new]) { [weak self] item, _ in
            let status = item.status
            Task { @MainActor in
                guard let self else { return }
                switch status {
                case .readyToPlay:
                    self.isReady = true
                case .failed:
                    self.isReady = false
                    self.handleItemFailure()
                default:
                    self.isReady = false
                }
            }
        }
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
