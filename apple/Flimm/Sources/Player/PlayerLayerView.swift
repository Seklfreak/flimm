import AVFoundation
import AVKit
import SwiftUI
import UIKit

/// A bare `AVPlayerLayer` in a `UIView`.
///
/// `VideoPlayer` would bring AVKit's own transport bar, and this app draws its
/// own scrubber (chapter ticks, SponsorBlock tints), so the layer is used
/// directly. It is also what `AVPictureInPictureController` needs.
class PlayerLayerUIView: UIView {
    override class var layerClass: AnyClass { AVPlayerLayer.self }

    var playerLayer: AVPlayerLayer? { layer as? AVPlayerLayer }
}

struct PlayerSurface: UIViewRepresentable {
    let engine: PlayerEngine

    func makeUIView(context: Context) -> PlayerLayerUIView {
        let view = PlayerLayerUIView()
        view.backgroundColor = .black
        view.playerLayer?.player = engine.player
        view.playerLayer?.videoGravity = .resizeAspect
        if let layer = view.playerLayer {
            engine.attach(layer: layer)
        }
        return view
    }

    func updateUIView(_ uiView: PlayerLayerUIView, context: Context) {
        if uiView.playerLayer?.player !== engine.player {
            uiView.playerLayer?.player = engine.player
        }
    }

    static func dismantleUIView(_ uiView: PlayerLayerUIView, coordinator: ()) {
        uiView.playerLayer?.player = nil
    }
}

/// Tracks whether Picture in Picture is running so the shell can dim its own
/// controls while the video is elsewhere.
final class PiPObserver: NSObject, AVPictureInPictureControllerDelegate {
    var onChange: ((Bool) -> Void)?

    func pictureInPictureControllerDidStartPictureInPicture(_ controller: AVPictureInPictureController) {
        onChange?(true)
    }

    func pictureInPictureControllerDidStopPictureInPicture(_ controller: AVPictureInPictureController) {
        onChange?(false)
    }

    func pictureInPictureController(
        _ controller: AVPictureInPictureController,
        failedToStartPictureInPictureWithError error: any Error
    ) {
        onChange?(false)
    }
}
