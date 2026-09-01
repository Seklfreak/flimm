// swiftlint:disable file_length
// A watching session is one object on purpose: everything here guards the
// same private playback state, and splitting the file means loosening the
// access that keeps that state coherent — the phone's ``WatchModel`` says the
// same thing for the same reason.
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
    /// ``upNext`` holds similar videos rather than the rest of the list: the
    /// context ran out. Offered, never queued — see ``UpNextPage``. The TV has
    /// no up-next list to show them in, so here the flag only keeps autoplay
    /// and the end card from walking into a guess.
    private(set) var upNextAreSuggestions = false
    private(set) var cues: [SubtitleCue] = []
    private(set) var activeCue: String?
    private(set) var loadError: String?
    /// The video played to its end and nothing took over — autoplay is off, or
    /// there was nothing left to play. See ``PlaybackEnd``; the overlay says
    /// so until playback moves again.
    private(set) var hasEnded = false
    /// Set only when the video has nowhere to go: a codec this device cannot
    /// decode on a server that predates `hls_url`. ``CodecGate`` decides.
    private(set) var codecIssue: CodecGate.Issue?
    /// True while the compatible H.264/AAC rendition is playing instead of the
    /// archived file.
    private(set) var usingCompatibleRendition = false
    /// The server-reported state of that rendition, refreshed on every attempt.
    private(set) var compatibleState: HLSState?
    /// How far it has been encoded, 0…1 — what turns the preparing overlay
    /// into "Preparing… 37%". 0 when the server has not said.
    private(set) var compatibleProgress: Double = 0
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
    /// Not private: the comments tab in the Info panel is built beside this
    /// model rather than inside the SwiftUI environment, so it takes the
    /// session's client from here.
    @ObservationIgnored let client: APIClient
    /// Per-device, unlike ``prefs``: a TV on ethernet wants a different answer
    /// from a phone on cellular, so quality never goes to `PATCH /me/prefs`.
    @ObservationIgnored private let playback: PlaybackSettings
    @ObservationIgnored private let reporter: ProgressReporter
    /// Whether the server accepted a heartbeat this session; see the phone's
    /// `WatchModel` for why that alone stales every cached list.
    @ObservationIgnored private var reportedProgress = false
    @ObservationIgnored private var startAtOverride: Double?
    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var endObserver: (any NSObjectProtocol)?
    /// Mutes and unmutes for SponsorBlock `mute` segments, keeping the
    /// viewer's own mute setting intact.
    @ObservationIgnored private lazy var services = PlaybackServices(client: client, platform: "tvos")
    /// Where to start, until the item reports `readyToPlay` and the seek can
    /// actually be issued. One seek, once — the compatible rendition is a
    /// complete VOD playlist from its first request, so seeking anywhere in it
    /// works exactly as it does in the archived file.
    @ObservationIgnored private var pendingSeek: Double?
    @ObservationIgnored private var statusObservation: NSKeyValueObservation?
    /// When the current run of attempts at the compatible rendition began. It
    /// rolls forward while the rendition actually plays, so a mid-playback
    /// stumble gets its own window rather than inheriting a spent one.
    @ObservationIgnored private var compatibleSince: Date?
    @ObservationIgnored private var compatibleRetry: Task<Void, Never>?
    /// Keeps the transcode pointed where the viewer is, and says how far it
    /// has got. Only the compatible rendition uses it.
    @ObservationIgnored private let steering: RenditionSteering
    /// Publishes this session so a phone can steer it, and applies what the
    /// phone presses. It runs its own clock — a paused `AVPlayer` stops
    /// ticking, and a session nobody heard from expires — so nothing here has
    /// to remember to push at it. The device name is the one the viewer gave
    /// this Apple TV in Settings, which is what makes "Living Room" mean
    /// something on the phone.
    @ObservationIgnored private lazy var remote = RemotePublisher(
        client: client, device: UIDevice.current.name, platform: "tvos"
    )

    /// How long to keep retrying a playlist the server could not open. A
    /// segment that has not been encoded yet no longer lands here — it blocks
    /// server-side and arrives late — so this is the job failing to start at
    /// all: `503` with `Retry-After: 5`, several of them in a row.
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
        return CodecGate.archivePlays(video, on: .current)
    }
    var hasContext: Bool { context.source != nil }
    var canGoNext: Bool { nav?.next != nil || (!upNextAreSuggestions && !upNext.isEmpty) }
    /// What the end card names as coming next — what ``goNext()`` would play.
    var nextUp: VideoSummary? { nav?.next ?? (upNextAreSuggestions ? nil : upNext.first) }
    var canGoPrevious: Bool { nav?.previous != nil }
    var currentTime: Double { player.currentTime().seconds.isFinite ? player.currentTime().seconds : 0 }
    /// What the heartbeat reports. An item that has not reported ready has no
    /// clock yet — it reads 0 — so until it does, the position playback is
    /// about to start from is what goes back, rather than a zero that would
    /// overwrite a good server-held position with a worse one.
    var reportedPosition: Double { isReady ? currentTime : (pendingSeek ?? currentTime) }
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
        self.steering = RenditionSteering(client: app.client)
        wireSteering()
        observeTime()
        beginPublishing()
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
    /// asked for, and the compatible H.264/AAC rendition when neither — a real
    /// transcode, which is why ``CodecGate`` is the only thing allowed to
    /// choose it. `AVPlayerViewController` plays HLS natively, so nothing else
    /// about the screen changes.
    private func startPlayback(_ detail: Video) async {
        services.startingVideo()
        compatibleRetry?.cancel()
        compatibleRetry = nil
        steering.cancel()
        audioUnavailable = false
        codecIssue = nil
        usingCompatibleRendition = false
        activeVariant = nil
        isReady = false

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
                // Only the height about to play is started: the server runs one
                // transcode at a time.
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
        Analytics.play(videoID: detail.id, kind: detail.type.rawValue, audioOnly: audioOnly)
        itemGeneration += 1
        if usingCompatibleRendition {
            // "Waiting" is either overlay: nothing on screen yet, or a stall
            // on a segment the encoder has not reached.
            steering.poll { [weak self] in
                guard let self else { return false }
                return !self.isReady || self.player.timeControlStatus == .waitingToPlayAtSpecifiedRate
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

    // MARK: - Transport

    func seek(to seconds: Double) {
        hasEnded = false
        let target = max(0, seconds)
        // An explicit seek replaces a resume that has not landed yet.
        pendingSeek = nil
        player.seek(to: CMTime(seconds: target, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
        steering.steer(to: target)
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
        await app.videoListStateChanged()
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

    /// Wired to the "Next video" transport-bar button and the matching
    /// Info-panel action (`TVPlayerViewController`), and to autoplay when an
    /// item ends. The remote's own skip gesture stays AVKit's default — moving
    /// ±10s inside this video — so the scrubber is never taken from the
    /// viewer.
    func goNext() async {
        if let next = nav?.next {
            await go(to: next.id)
        } else if !upNextAreSuggestions, let next = upNext.first {
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
        hasEnded = false
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
        // Before anything else the phone reads: a controller must learn the
        // screen went dark now, not when the session expires.
        await remote.stop()
        services.stop()
        steering.cancel()
        await reporter.stop()
        // After the last heartbeat: the list underneath reloads once this
        // returns, and its in-progress row has to show where playback stopped.
        if reportedProgress { await app.videoListStateChanged() }
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

    /// Everything the server says about the job while it is steered or polled
    /// lands here, so the overlay's percentage has one source.
    private func wireSteering() {
        steering.onStatus = { [weak self] status in
            self?.compatibleState = status.state
            self?.compatibleProgress = status.progress
        }
    }

    private func observeTime() {
        let interval = CMTime(seconds: 0.2, preferredTimescale: 600)
        timeObserver = player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { [weak self] time in
            MainActor.assumeIsolated { self?.tick(time.seconds) }
        }
    }

    private func tick(_ seconds: Double) {
        guard seconds.isFinite else { return }
        // AVKit owns the transport bar, so pressing play or scrubbing back
        // never reaches this model as a call — the tick is the notice that
        // the video is no longer sitting on its last frame.
        if hasEnded, player.timeControlStatus == .playing { hasEnded = false }
        // The retry window measures "how long without playback", so it rolls
        // forward while the rendition is actually playing.
        if usingCompatibleRendition, isReady { compatibleSince = Date() }
        // The "Resumed from …" offer retires itself once it has been on
        // screen for a minute of playback; see ``ResumeNotice``.
        resumedFrom = ResumeNotice.retained(resumedFrom, currentTime: seconds)
        activeCue = WebVTT.cue(at: seconds, in: cues)?.text
        // The same three rules the phone follows, from the same place.
        let sponsor = services.tick(
            .init(
                videoID: videoId,
                time: seconds,
                isStalled: player.timeControlStatus == .waitingToPlayAtSpecifiedRate,
                height: activeVariant?.height ?? 0,
                segments: video?.sponsorblock ?? [],
                prefs: prefs,
                isMuted: player.isMuted
            ),
            setGain: { [weak self] gain in self?.player.volume = LoudnessGain.volume(forGainDB: gain) }
        )
        if let to = sponsor.skipTo { player.seek(to: CMTime(seconds: to, preferredTimescale: 600)) }
        if let muted = sponsor.muted { player.isMuted = muted }
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
                    // The one seek a resume needs: issuing it before the item
                    // is ready is silently dropped, so it waits for exactly
                    // this moment — and then lands, wherever it points.
                    self.applyPendingSeek()
                case .failed:
                    self.isReady = false
                    self.handleItemFailure()
                default:
                    self.isReady = false
                }
            }
        }
    }

    private func applyPendingSeek() {
        guard let pending = pendingSeek else { return }
        pendingSeek = nil
        player.seek(to: CMTime(seconds: pending, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
    }

    /// Autoplay advances; anything else stops on the end card, which
    /// ``TVPlayerOverlay`` draws. ``PlaybackEnd`` is the rule itself, shared
    /// with the phone and the web so the three cannot drift on when a viewer
    /// is told their video is over.
    private func handleEnded() async {
        await reporter.flush()
        switch PlaybackEnd.decide(autoplay: prefs.autoplay, hasNext: canGoNext) {
        case .advance: await goNext()
        case .finished: hasEnded = true
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

}

/// The rest of what a video needs, once it is playing: chapters, the
/// navigation around it, subtitles and artwork. None of it blocks the
/// picture, and none of it decides anything — which is why it sits outside
/// the model's own body rather than in it.
private extension TVWatchModel {
    func loadSidecars(_ detail: Video) async {
        async let chapterList = fetchChapters()
        async let navigation = fetchNav()
        async let next = fetchUpNext()
        let (loadedChapters, loadedNav, loadedNext) = await (chapterList, navigation, next)
        chapters = loadedChapters
        nav = loadedNav
        upNext = loadedNext.items
        upNextAreSuggestions = loadedNext.suggestions
        interstitials = TVPlayerMarkers.interstitials(for: detail.sponsorblock)
        itemGeneration += 1
        await loadSubtitles(detail)
        await loadArtwork(detail)
    }

    func fetchChapters() async -> [Chapter] {
        (try? await client.chapters(videoId))?.chapters ?? []
    }

    /// Without a context there is no list to step through, so the player hides
    /// the previous/next controls rather than guessing at neighbours.
    func fetchNav() async -> Nav? {
        guard hasContext else { return nil }
        return try? await client.nav(videoId, context: context)
    }

    func fetchUpNext() async -> UpNextPage {
        (try? await client.upNext(videoId, context: context)) ?? UpNextPage(page: Page(items: []))
    }

    func loadSubtitles(_ detail: Video) async {
        guard let track = SubtitleLoader.pick(from: detail.subtitles, preferred: prefs.subtitleLang) else {
            cues = []
            return
        }
        cues = await SubtitleLoader.load(track: track, client: client)
    }

    func loadArtwork(_ detail: Video) async {
        artwork = await MediaImageStore.shared.image(at: detail.thumbUrl, client: client)
        pushNowPlaying(force: true)
    }
}

/// Remote control: this screen, published so a phone can steer it.
///
/// It reads the model's private playback state, which is why it is in the
/// same file; it is out of the class body only because that body is already
/// as long as it is allowed to be.
private extension TVWatchModel {
    /// Starts saying what this screen is playing, and listening for a phone.
    ///
    /// Started once, for the life of the model rather than of a video: stepping
    /// to the next video is the same session to whoever is holding the phone,
    /// and re-registering per video would make it blink out of their hands
    /// between the two.
    func beginPublishing() {
        remote.start(
            state: { [weak self] in self?.remoteState },
            onCommand: { [weak self] command in self?.apply(command) }
        )
    }

    /// What a controller sees. `nil` until something is actually loaded, which
    /// is what keeps a session from existing before there is anything to steer.
    var remoteState: RemoteSession? {
        guard let video else { return nil }
        return RemoteSession(
            videoId: videoId,
            title: video.title,
            channelName: video.channel.name,
            thumbUrl: video.thumbUrl,
            position: reportedPosition,
            duration: video.duration,
            // Buffering is not pausing: the picture is stopped but the viewer
            // did not stop it, and a phone showing "paused" would offer to
            // resume something that is already trying to.
            paused: player.timeControlStatus == .paused,
            // The rate playback is *meant* to run at. `player.rate` is 0 while
            // paused, and a controller reading that would stop its own clock
            // twice over.
            speed: prefs.playbackSpeed,
            audioOnly: audioOnly,
            // Whether stepping is possible is this player's answer, from its
            // own context; the phone must never derive it.
            canNext: canGoNext,
            canPrevious: canGoPrevious
        )
    }

    /// Applies what the phone pressed.
    ///
    /// Every one of these is the same call the Siri Remote and the Info panel
    /// make, so a command cannot reach a path the television's own controls
    /// cannot — including the heartbeat and the sponsor rules that hang off
    /// ``seek(to:)``.
    func apply(_ command: RemoteCommand) {
        switch command.action {
        case .play:
            hasEnded = false
            player.playImmediately(atRate: Float(prefs.playbackSpeed))
        case .pause:
            player.pause()
        case .seek:
            seek(to: command.position)
        case .skip:
            // A delta rather than a position, because the phone's clock is a
            // projection: ±10s from where playback *actually* is can only be
            // worked out here.
            seek(to: currentTime + command.delta)
        case .next:
            Task { await goNext() }
        case .previous:
            Task { await goPrevious() }
        case nil:
            break
        }
    }
}

private extension TVWatchModel {
    /// Only meaningful for audio-only playback; with a video on screen
    /// `AVPlayerViewController` publishes its own metadata.
    func pushNowPlaying(force: Bool) {
        guard audioOnly, let video else { return }
        let itemDuration = player.currentItem?.duration.seconds ?? 0
        TVNowPlaying.update(TVNowPlayingState(
            title: video.title,
            artist: video.channel.name,
            duration: itemDuration.isFinite && itemDuration > 0 ? itemDuration : video.duration,
            position: currentTime,
            rate: player.rate == 0 ? 0 : prefs.playbackSpeed,
            artwork: artwork
        ), force: force)
    }

    /// The other way a video becomes seen: playback reaches the end and the
    /// heartbeat comes back `watched`. The lists behind the player are then as
    /// stale as after an explicit "Mark seen", and need the same invalidation.
    func applyProgress(_ result: ProgressResult) async {
        reportedProgress = true
        guard result.watched, !isWatched else { return }
        isWatched = true
        await app.videoListStateChanged()
    }
}
