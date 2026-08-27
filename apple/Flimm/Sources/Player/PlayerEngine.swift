import AVFoundation
import AVKit
import FlimmKit
import Foundation
import Observation

/// A thin, observable wrapper around one `AVPlayer`.
///
/// It owns nothing about Flimm: no watch state, no context, no rules about what
/// counts as watched. It plays a URL with headers, reports time, and seeks.
@MainActor
@Observable
final class PlayerEngine {
    let player = AVPlayer()

    private(set) var currentTime: Double = 0
    private(set) var duration: Double = 0
    private(set) var isPlaying = false
    private(set) var isBuffering = false
    private(set) var isPiPActive = false
    private(set) var isPiPPossible = false
    private(set) var isMuted = false
    /// True once the current item reports `readyToPlay` — the moment a
    /// "preparing…" state has something real to hand over to.
    private(set) var isReady = false
    /// Set when the item itself failed — a bad URL, a 401, an unsupported
    /// file, or a rendition whose playlist the server could not open yet.
    private(set) var failure: String?

    @ObservationIgnored var onEnded: (@MainActor () -> Void)?
    /// Fired with the same message as ``failure``, so an owner can retry
    /// rather than surface it — which is what the HLS rendition needs while
    /// the transcode is still catching up.
    @ObservationIgnored var onFailed: (@MainActor (String) -> Void)?
    /// Fired roughly five times a second while playing — where sponsor
    /// skipping, subtitle cues and the Now Playing info hang off.
    @ObservationIgnored var onTick: (@MainActor (Double) -> Void)?

    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var endObserver: (any NSObjectProtocol)?
    @ObservationIgnored private var failObserver: (any NSObjectProtocol)?
    @ObservationIgnored private var statusObservation: NSKeyValueObservation?
    @ObservationIgnored private var controlObservation: NSKeyValueObservation?
    @ObservationIgnored private var pipObservation: NSKeyValueObservation?
    @ObservationIgnored private var pip: AVPictureInPictureController?
    @ObservationIgnored private let pipObserver = PiPObserver()
    /// Where to start, until the item reports `readyToPlay` and the seek can
    /// actually be issued. One seek, once — the compatible rendition is a
    /// complete VOD playlist from its first request, so seeking anywhere in it
    /// works exactly as it does in the archived file.
    @ObservationIgnored private var pendingSeek: Double?
    @ObservationIgnored private var desiredRate: Double = 1

    init() {
        player.automaticallyWaitsToMinimizeStalling = true
        observeTime()
        observeControlStatus()
        pipObserver.onChange = { [weak self] active in
            Task { @MainActor in self?.isPiPActive = active }
        }
    }

    // MARK: - Loading

    /// ``APIClient/assetHTTPHeaderFieldsKey`` is how the bearer token reaches
    /// `/media`, including on every byte-range request seeking makes and on
    /// every segment of an HLS rendition.
    ///
    /// The compatible rendition needs no special case: its playlist is a
    /// complete VOD one from the first request, so the item's duration is the
    /// video's own and `startAt` lands wherever it is asked to.
    func load(
        url: URL,
        headers: [String: String],
        startAt: Double,
        rate: Double,
        duration knownDuration: Double
    ) {
        failure = nil
        isReady = false
        desiredRate = rate
        duration = knownDuration
        currentTime = startAt
        pendingSeek = startAt > 0 ? startAt : nil

        let asset = AVURLAsset(url: url, options: [APIClient.assetHTTPHeaderFieldsKey: headers])
        let item = AVPlayerItem(asset: asset)
        removeItemObservers()
        observeStatus(of: item)
        observeFailure(of: item)
        observeEnd(of: item)
        player.replaceCurrentItem(with: item)
        player.playImmediately(atRate: Float(rate))
        isPlaying = true
    }

    func unload() {
        player.pause()
        player.replaceCurrentItem(with: nil)
        isPlaying = false
        isReady = false
    }

    // MARK: - Transport

    func play() {
        player.playImmediately(atRate: Float(desiredRate))
        isPlaying = true
    }

    func pause() {
        player.pause()
        isPlaying = false
    }

    func togglePlay() {
        if isPlaying { pause() } else { play() }
    }

    /// `m` on a hardware keyboard, and the item in the options menu.
    func toggleMute() {
        player.isMuted.toggle()
        isMuted = player.isMuted
    }

    /// Mute driven by something other than the viewer — a SponsorBlock `mute`
    /// segment. Separate from ``toggleMute()`` so what the segment restores
    /// afterwards is the viewer's own setting.
    func setMuted(_ muted: Bool) {
        player.isMuted = muted
        isMuted = muted
    }

    func setRate(_ rate: Double) {
        desiredRate = rate
        if isPlaying { player.rate = Float(rate) }
    }

    func seek(to seconds: Double) {
        let target = min(max(0, seconds), duration > 0 ? duration : seconds)
        currentTime = target
        guard player.currentItem?.status == .readyToPlay else {
            pendingSeek = target
            return
        }
        // An explicit seek replaces a resume that has not landed yet.
        pendingSeek = nil
        player.seek(to: CMTime(seconds: target, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
    }

    func skip(by delta: Double) {
        seek(to: currentTime + delta)
    }

    // MARK: - Picture in Picture

    func attach(layer: AVPlayerLayer) {
        guard AVPictureInPictureController.isPictureInPictureSupported() else { return }
        let controller = AVPictureInPictureController(playerLayer: layer)
        controller?.delegate = pipObserver
        pip = controller
        pipObservation = controller?.observe(\.isPictureInPicturePossible, options: [.initial, .new]) { [weak self] control, _ in
            let possible = control.isPictureInPicturePossible
            Task { @MainActor in self?.isPiPPossible = possible }
        }
    }

    func togglePiP() {
        guard let pip, pip.isPictureInPicturePossible else { return }
        if pip.isPictureInPictureActive {
            pip.stopPictureInPicture()
        } else {
            pip.startPictureInPicture()
        }
    }

    /// PiP is meaningless without a video layer, so audio-only playback drops it.
    func detachPiP() {
        pipObservation?.invalidate()
        pipObservation = nil
        pip = nil
        isPiPPossible = false
    }

    // MARK: - Teardown

    func tearDown() {
        if let timeObserver { player.removeTimeObserver(timeObserver) }
        timeObserver = nil
        removeItemObservers()
        controlObservation?.invalidate()
        detachPiP()
        unload()
    }

    // MARK: - Observation

    private func observeTime() {
        let interval = CMTime(seconds: 0.2, preferredTimescale: 600)
        timeObserver = player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { [weak self] time in
            MainActor.assumeIsolated { self?.tick(time.seconds) }
        }
    }

    private func tick(_ seconds: Double) {
        guard seconds.isFinite else { return }
        if let item = player.currentItem, item.status == .readyToPlay {
            // The archived duration is authoritative until the item reports a
            // better one; the rendition's playlist carries the whole video, so
            // that one agrees rather than shrinking the scrubber.
            let itemDuration = item.duration.seconds
            if itemDuration.isFinite, itemDuration > 0 { duration = itemDuration }
        } else if pendingSeek != nil {
            // The item has not reported ready, so its clock says 0 rather than
            // anything true. Letting that through would overwrite the position
            // playback is about to start from — which is how a resume is lost
            // when the item fails and is reopened, or while the first segment
            // of a rendition is still being encoded.
            return
        }
        currentTime = seconds
        onTick?(seconds)
    }

    private func observeControlStatus() {
        controlObservation = player.observe(\.timeControlStatus, options: [.initial, .new]) { [weak self] player, _ in
            let status = player.timeControlStatus
            Task { @MainActor in
                self?.isBuffering = status == .waitingToPlayAtSpecifiedRate
                if status == .playing { self?.isPlaying = true }
            }
        }
    }

    /// `readyToPlay` is what ends a "preparing…" state, and `.failed` is the
    /// only signal a playlist the server has not finished yet produces — it
    /// never reaches `failedToPlayToEndTime`, because it never started.
    private func observeStatus(of item: AVPlayerItem) {
        statusObservation = item.observe(\.status, options: [.initial, .new]) { [weak self] item, _ in
            let status = item.status
            let message = (item.error as NSError?)?.localizedDescription ?? "Playback failed."
            Task { @MainActor in
                guard let self else { return }
                switch status {
                case .readyToPlay:
                    self.isReady = true
                    // The one seek a resume needs. Issuing it before the item
                    // is ready is silently dropped, so it waits for exactly
                    // this moment — and then lands, wherever it points.
                    if let pending = self.pendingSeek {
                        self.pendingSeek = nil
                        self.player.seek(
                            to: CMTime(seconds: pending, preferredTimescale: 600),
                            toleranceBefore: .zero,
                            toleranceAfter: .zero
                        )
                    }
                case .failed:
                    self.isReady = false
                    self.failure = message
                    self.isPlaying = false
                    self.onFailed?(message)
                default:
                    self.isReady = false
                }
            }
        }
    }

    private func observeEnd(of item: AVPlayerItem) {
        endObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.didPlayToEndTimeNotification,
            object: item,
            queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.isPlaying = false
                self?.onEnded?()
            }
        }
    }

    private func observeFailure(of item: AVPlayerItem) {
        failObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.failedToPlayToEndTimeNotification,
            object: item,
            queue: .main
        ) { [weak self] note in
            let error = note.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? NSError
            let message = error?.localizedDescription ?? "Playback failed."
            MainActor.assumeIsolated {
                self?.failure = message
                self?.isPlaying = false
                self?.onFailed?(message)
            }
        }
    }

    private func removeItemObservers() {
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        if let failObserver { NotificationCenter.default.removeObserver(failObserver) }
        endObserver = nil
        failObserver = nil
        statusObservation?.invalidate()
        statusObservation = nil
    }
}
