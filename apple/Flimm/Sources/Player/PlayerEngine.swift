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
    /// Set when the item itself failed — a bad URL, a 401, an unsupported file.
    private(set) var failure: String?

    @ObservationIgnored var onEnded: (@MainActor () -> Void)?
    /// Fired roughly five times a second while playing — where sponsor
    /// skipping, subtitle cues and the Now Playing info hang off.
    @ObservationIgnored var onTick: (@MainActor (Double) -> Void)?

    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var endObserver: (any NSObjectProtocol)?
    @ObservationIgnored private var failObserver: (any NSObjectProtocol)?
    @ObservationIgnored private var controlObservation: NSKeyValueObservation?
    @ObservationIgnored private var pipObservation: NSKeyValueObservation?
    @ObservationIgnored private var pip: AVPictureInPictureController?
    @ObservationIgnored private let pipObserver = PiPObserver()
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
    /// `/media`, including on every byte-range request seeking makes.
    func load(url: URL, headers: [String: String], startAt: Double, rate: Double, duration knownDuration: Double) {
        failure = nil
        desiredRate = rate
        duration = knownDuration
        currentTime = startAt
        pendingSeek = startAt > 0 ? startAt : nil

        let asset = AVURLAsset(url: url, options: [APIClient.assetHTTPHeaderFieldsKey: headers])
        let item = AVPlayerItem(asset: asset)
        removeItemObservers()
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
            if let pending = pendingSeek {
                pendingSeek = nil
                player.seek(to: CMTime(seconds: pending, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
                return
            }
            // The archived duration is authoritative, but a live-ish item can
            // report a better one.
            let itemDuration = item.duration.seconds
            if itemDuration.isFinite, itemDuration > 0 { duration = itemDuration }
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
            }
        }
    }

    private func removeItemObservers() {
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        if let failObserver { NotificationCenter.default.removeObserver(failObserver) }
        endObserver = nil
        failObserver = nil
    }
}
